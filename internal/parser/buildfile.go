package parser

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	pluginAliasRe   = regexp.MustCompile(`alias\s*\(\s*libs\.plugins\.([\w.]+)\s*\)`)
	pluginIDRe      = regexp.MustCompile(`id\s*\(\s*"([^"]*)"\s*(?:,\s*"([^"]*)")?\s*\)`)
	pluginApplyRe   = regexp.MustCompile(`apply\s*\(\s*plugin\s*=\s*"([^"]*)"\s*\)`)
	compileSdkNewRe = regexp.MustCompile(`release\s*\(\s*(\d+)\s*\)\s*\{?[^}]*minorApiLevel\s*=\s*(\d+)`)
	compileSdkRe    = regexp.MustCompile(`release\s*\(\s*(\d+)\s*\)`)
	abiFilterRe     = regexp.MustCompile(`abiFilters\s*\+=\s*listOf\s*\(([^)]*)\)`)
	proguardFilesRe = regexp.MustCompile(`proguardFiles\s*\(([^)]*)\)`)
	signingCfgRe    = regexp.MustCompile(`signingConfig\s*=\s*signingConfigs\.getByName\s*\(\s*"([^"]*)"\s*\)`)
	javaVersionRe   = regexp.MustCompile(`JavaVersion\.VERSION_(\w+)`)
	bomDetectRe     = regexp.MustCompile(`platform\s*\(\s*libs\.([\w.]+)\s*\)`)
	scopeStmtRe     = regexp.MustCompile(`(?s)(\w+)\s*\(\s*(.+?)\s*\)\s*$`)
	platformShellRe = regexp.MustCompile(`(?s)platform\s*\(\s*(.+?)\s*\)\s*$`)
	libsAccessorRe  = regexp.MustCompile(`libs\.([\w.]+)\s*$`)
	coordStrRe      = regexp.MustCompile(`"([^:]+):([^:]+):([^"]+)"`)
	projectDepRe    = regexp.MustCompile(`project\s*\(\s*"([^"]*)"\s*\)`)
	excludeSingleRe = regexp.MustCompile(`excludes\s*\+=\s*"([^"]*)"`)
	excludeListRe   = regexp.MustCompile(`excludes\s*\+=\s*listOf\s*\(([^)]*)\)`)
	buildConfigFieldRe = regexp.MustCompile(`buildConfigField\s*\(\s*"([^"]*)"\s*,\s*"([^"]*)"\s*,\s*"([^"]*)"\s*\)`)
	createNameRe        = regexp.MustCompile(`create\s*\(\s*"([^"]*)"\s*\)`)
	versionNameSuffixRe = regexp.MustCompile(`versionNameSuffix\s*=\s*"([^"]*)"`)
)

var buildFeaturesKeys = []string{
	"compose", "viewBinding", "dataBinding", "buildConfig",
	"aidl", "renderScript", "resValues", "shaders",
}

// BuildFileParser build.gradle.kts 解析器
type BuildFileParser struct {
	Script GradleScriptParser
}

// extractCreateBlockBody 从 productFlavors 块内容中提取第一个 create("name") { ... } 的块体
func extractCreateBlockBody(parentContent string) string {
	re := regexp.MustCompile(`create\s*\(\s*"[^"]*"\s*\)\s*\{`)
	loc := re.FindStringIndex(parentContent)
	if loc == nil {
		return ""
	}
	start := loc[1]
	depth := 1
	i := start
	for i < len(parentContent) && depth > 0 {
		c := parentContent[i]
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return parentContent[start:i]
			}
		}
		i++
	}
	return ""
}

// Parse 解析 build.gradle.kts
func (p BuildFileParser) Parse(buildPath string, catalog *VersionCatalog, gradleProps map[string]string) ModuleConfig {
	parent := parentDir(buildPath)
	module := ModuleConfig{
		Name:           filepath.Base(parent),
		Path:           parent,
		PluginVersions: map[string]string{},
		BuildFeatures:  map[string]bool{},
	}
	if module.Name == "" {
		module.Name = "."
	}
	content, err := os.ReadFile(buildPath)
	if err != nil {
		return module
	}
	text := p.Script.StripComments(string(content))

	// 替换 gradle.properties 变量
	for k, v := range gradleProps {
		text = strings.ReplaceAll(text, "${"+k+"}", v)
		text = strings.ReplaceAll(text, "$"+k, v)
	}

	p.parsePlugins(text, catalog, &module)
	p.parseAndroidBlock(text, &module)
	p.parseDependencies(text, catalog, &module)
	p.parseBuildFeatures(text, &module)
	p.parsePackaging(text, &module)
	return module
}

func parentDir(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return p
	}
	return p[:idx]
}

func (p BuildFileParser) parsePlugins(content string, catalog *VersionCatalog, module *ModuleConfig) {
	block, ok := p.Script.FindBlock(content, "plugins")
	if !ok {
		return
	}
	for _, stmt := range p.Script.GetTopLevelStatements(block) {
		if m := pluginAliasRe.FindStringSubmatch(stmt); m != nil {
			if id, ver, ok := catalog.ResolvePlugin(m[1]); ok {
				module.Plugins = append(module.Plugins, id)
				module.PluginVersions[id] = ver
			}
			continue
		}
		if m := pluginIDRe.FindStringSubmatch(stmt); m != nil {
			module.Plugins = append(module.Plugins, m[1])
			if len(m) > 2 && m[2] != "" {
				module.PluginVersions[m[1]] = m[2]
			}
			continue
		}
		if m := pluginApplyRe.FindStringSubmatch(stmt); m != nil {
			module.Plugins = append(module.Plugins, m[1])
		}
	}
}

func (p BuildFileParser) parseAndroidBlock(content string, module *ModuleConfig) {
	block, ok := p.Script.FindBlock(content, "android")
	if !ok {
		return
	}
	android := &AndroidConfig{}

	if v, ok := p.Script.ExtractStringValue(block, "namespace"); ok {
		android.Namespace = v
	}
	if v, ok := p.Script.ExtractIntValue(block, "compileSdk"); ok {
		f := float64(v)
		android.CompileSDK = &f
	} else if v, ok := p.Script.ExtractIntValue(block, "compileSdkVersion"); ok {
		f := float64(v)
		android.CompileSDK = &f
	} else {
		// 新语法 compileSdk { version = release(37){ minorApiLevel = 1 } }
		if m := compileSdkNewRe.FindStringSubmatch(block); m != nil {
			major, _ := strconv.Atoi(m[1])
			minor, _ := strconv.Atoi(m[2])
			f := float64(major) + float64(minor)/10
			android.CompileSDK = &f
		} else if m := compileSdkRe.FindStringSubmatch(block); m != nil {
			v, _ := strconv.Atoi(m[1])
			f := float64(v)
			android.CompileSDK = &f
		}
	}

	if dc, ok := p.Script.FindBlock(block, "defaultConfig"); ok {
		if v, ok := p.Script.ExtractStringValue(dc, "applicationId"); ok {
			android.ApplicationID = v
		}
		if v, ok := p.Script.ExtractIntValue(dc, "minSdk"); ok {
			android.MinSDK = &v
		}
		if v, ok := p.Script.ExtractIntValue(dc, "targetSdk"); ok {
			android.TargetSDK = &v
		}
		if v, ok := p.Script.ExtractIntValue(dc, "versionCode"); ok {
			android.VersionCode = &v
		}
		if v, ok := p.Script.ExtractStringValue(dc, "versionName"); ok {
			android.VersionName = v
		}
		if v, ok := p.Script.ExtractStringValue(dc, "testInstrumentationRunner"); ok {
			android.TestInstrumentationRunner = v
		}
	}

	if ndk, ok := p.Script.FindBlock(block, "ndk"); ok {
		if m := abiFilterRe.FindStringSubmatch(ndk); m != nil {
			android.ABIFilters = p.Script.ExtractQuotedStrings(m[1])
		}
	}

	// productFlavors：取第一个 flavor 的 buildConfigField（默认 full/included 变体）
	if pf, ok := p.Script.FindBlock(block, "productFlavors"); ok {
		// create("name") { ... } 形式用正则直接提取块
		if m := createNameRe.FindStringSubmatch(pf); m != nil {
			android.SelectedFlavor = m[1]
		}
		// 提取第一个 create 块体内文本
		if first := extractCreateBlockBody(pf); first != "" {
			for _, m := range buildConfigFieldRe.FindAllStringSubmatch(first, -1) {
				android.BuildConfigFields = append(android.BuildConfigFields,
					m[1]+":"+m[2]+":"+m[3])
			}
			if m := versionNameSuffixRe.FindStringSubmatch(first); m != nil {
				android.FlavorVersionNameSuffix = m[1]
			}
		}
	}

	if bt, ok := p.Script.FindBlock(block, "buildTypes"); ok {
		if rel, ok2 := p.Script.FindBlock(bt, "release"); ok2 {
			if v, ok := p.Script.ExtractBoolValue(rel, "isMinifyEnabled"); ok {
				android.MinifyEnabled = v
			}
			if v, ok := p.Script.ExtractBoolValue(rel, "minifyEnabled"); ok {
				android.MinifyEnabled = v
			}
			if v, ok := p.Script.ExtractBoolValue(rel, "isShrinkResources"); ok {
				android.ShrinkResources = v
			}
			if v, ok := p.Script.ExtractBoolValue(rel, "shrinkResources"); ok {
				android.ShrinkResources = v
			}
			if m := proguardFilesRe.FindStringSubmatch(rel); m != nil {
				android.ProguardFiles = p.Script.ExtractQuotedStrings(m[1])
			}
			if m := signingCfgRe.FindStringSubmatch(rel); m != nil {
				android.SigningConfig = m[1]
			}
		}
	}

	if co, ok := p.Script.FindBlock(block, "compileOptions"); ok {
		sc, okSC := p.Script.ExtractStringValue(co, "sourceCompatibility")
		tc, okTC := p.Script.ExtractStringValue(co, "targetCompatibility")
		if okSC {
			android.SourceCompatibility = sc
		} else if m := javaVersionRe.FindStringSubmatch(co); m != nil {
			android.SourceCompatibility = m[1]
		}
		if okTC {
			android.TargetCompatibility = tc
		} else if m := javaVersionRe.FindStringSubmatch(co); m != nil {
			android.TargetCompatibility = m[1]
		}
	}

	if ko, ok := p.Script.FindBlock(block, "kotlinOptions"); ok {
		if v, ok := p.Script.ExtractStringValue(ko, "jvmTarget"); ok {
			android.JvmTarget = v
		}
	}

	module.Android = android
}

func (p BuildFileParser) parseDependencies(content string, catalog *VersionCatalog, module *ModuleConfig) {
	block, ok := p.Script.FindBlock(content, "dependencies")
	if !ok {
		return
	}
	boms := map[string]string{}
	for _, stmt := range p.Script.GetTopLevelStatements(block) {
		if m := bomDetectRe.FindStringSubmatch(stmt); m != nil {
			if g, _, v, ok := catalog.ResolveLibrary(m[1]); ok && v != "" {
				boms[g] = v
			}
		}
	}
	for _, stmt := range p.Script.GetTopLevelStatements(block) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "//") {
			continue
		}
		if dep := p.parseDependencyStatement(stmt, catalog, boms); dep != nil {
			module.Dependencies = append(module.Dependencies, *dep)
		}
	}
}

func (p BuildFileParser) parseDependencyStatement(stmt string, catalog *VersionCatalog, boms map[string]string) *Dependency {
	m := scopeStmtRe.FindStringSubmatch(stmt)
	if m == nil {
		return nil
	}
	scope := m[1]
	inner := m[2]
	isPlatform := false
	if pm := platformShellRe.FindStringSubmatch(inner); pm != nil {
		inner = pm[1]
		isPlatform = true
	}
	dep := &Dependency{Scope: scope, IsPlatform: isPlatform}

	if lm := libsAccessorRe.FindStringSubmatch(inner); lm != nil {
		g, a, v, ok := catalog.ResolveWithBom(lm[1], boms)
		if !ok {
			return nil
		}
		dep.Group, dep.Artifact, dep.Version = g, a, v
		return dep
	}
	if cm := coordStrRe.FindStringSubmatch(inner); cm != nil {
		dep.Group, dep.Artifact, dep.Version = cm[1], cm[2], cm[3]
		return dep
	}
	if pm := projectDepRe.FindStringSubmatch(inner); pm != nil {
		dep.IsProject = true
		dep.ProjectPath = pm[1]
		return dep
	}
	return nil
}

func (p BuildFileParser) parseBuildFeatures(content string, module *ModuleConfig) {
	androidBlock, ok := p.Script.FindBlock(content, "android")
	if !ok {
		return
	}
	bf, ok := p.Script.FindBlock(androidBlock, "buildFeatures")
	if !ok {
		return
	}
	for _, key := range buildFeaturesKeys {
		if v, ok := p.Script.ExtractBoolValue(bf, key); ok {
			module.BuildFeatures[key] = v
		}
	}
}

func (p BuildFileParser) parsePackaging(content string, module *ModuleConfig) {
	androidBlock, ok := p.Script.FindBlock(content, "android")
	if !ok {
		return
	}
	pkg, ok := p.Script.FindBlock(androidBlock, "packaging")
	if !ok {
		return
	}
	res, ok := p.Script.FindBlock(pkg, "resources")
	if !ok {
		res = pkg
	}
	for _, m := range excludeSingleRe.FindAllStringSubmatch(res, -1) {
		module.PackagingExcludes = append(module.PackagingExcludes, m[1])
	}
	for _, m := range excludeListRe.FindAllStringSubmatch(res, -1) {
		module.PackagingExcludes = p.Script.ExtractQuotedStrings(m[1])
	}
}
