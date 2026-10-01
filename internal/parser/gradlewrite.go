package parser

import (
	"fmt"
	"path/filepath"
	"strings"
)

// GradleFiles 描述一组生成出的 Gradle 配置文件的相对路径 -> 内容。
type GradleFiles map[string]string

// GenerateGradleFiles 把 ProjectConfig 渲染为 Gradle 工程文件集合。
// 生成：settings.gradle.kts、gradle/libs.versions.toml，以及每个模块的 build.gradle.kts。
// files 的 key 为相对项目根的路径（使用 '/'）。
func GenerateGradleFiles(config *ProjectConfig) GradleFiles {
	files := GradleFiles{}
	files["settings.gradle.kts"] = generateSettings(config)
	if config.Catalog != nil && catalogHasContent(config.Catalog) {
		files["gradle/libs.versions.toml"] = generateVersionCatalog(config.Catalog)
	}
	for _, mod := range config.Modules {
		rel := moduleRelDir(config.RootDir, mod)
		path := filepath.ToSlash(filepath.Join(rel, "build.gradle.kts"))
		if rel == "" {
			path = "build.gradle.kts"
		}
		files[path] = generateBuildFile(mod, config.Catalog)
	}
	return files
}

func catalogHasContent(c *VersionCatalog) bool {
	return len(c.Versions) > 0 || len(c.Libraries) > 0 || len(c.Plugins) > 0 || len(c.Bundles) > 0
}

func moduleRelDir(root string, mod ModuleConfig) string {
	if mod.Path == "" {
		return mod.Name
	}
	if filepath.IsAbs(mod.Path) {
		if rel, err := filepath.Rel(root, mod.Path); err == nil {
			return filepath.ToSlash(rel)
		}
		return filepath.Base(mod.Path)
	}
	return filepath.ToSlash(mod.Path)
}

func generateSettings(config *ProjectConfig) string {
	var b strings.Builder
	b.WriteString("// 由 Canter (`canter convert --to-gradle`) 生成。\n")
	b.WriteString("pluginManagement {\n")
	b.WriteString("    repositories {\n")
	for _, r := range pluginRepoStatements(config.PluginRepositories) {
		fmt.Fprintf(&b, "        %s\n", r)
	}
	b.WriteString("    }\n}\n\n")
	b.WriteString("dependencyResolutionManagement {\n")
	b.WriteString("    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)\n")
	b.WriteString("    repositories {\n")
	for _, r := range repoStatements(config.Repositories) {
		fmt.Fprintf(&b, "        %s\n", r)
	}
	b.WriteString("    }\n}\n\n")
	if config.ProjectName != "" {
		fmt.Fprintf(&b, "rootProject.name = %s\n", quoteKotlin(config.ProjectName))
	}
	// composite build：同一声明只输出一次 includeBuild；其模块不再单独 include
	includedBuilds := map[string]bool{}
	for _, mod := range config.Modules {
		if mod.IncludeBuild != "" {
			includedBuilds[mod.IncludeBuild] = true
		}
	}
	for _, ib := range sortedMapKeys(includedBuilds) {
		fmt.Fprintf(&b, "includeBuild(%s)\n", quoteKotlin(ib))
	}
	for _, mod := range config.Modules {
		if mod.IncludeBuild != "" {
			continue
		}
		rel := moduleRelDir(config.RootDir, mod)
		inc := ":" + strings.ReplaceAll(rel, "/", ":")
		fmt.Fprintf(&b, "include(%s)\n", quoteKotlin(inc))
	}
	return b.String()
}

func repoStatements(urls []string) []string {
	var out []string
	for _, u := range urls {
		switch u {
		case "https://dl.google.com/dl/android/maven2":
			out = append(out, "google()")
		case "https://repo.maven.apache.org/maven2":
			out = append(out, "mavenCentral()")
		case "mavenLocal":
			out = append(out, "mavenLocal()")
		case "https://plugins.gradle.org/maven2":
			out = append(out, "maven { url = uri(\"https://plugins.gradle.org/maven2\") }")
		default:
			out = append(out, fmt.Sprintf("maven { url = uri(%s) }", quoteKotlin(u)))
		}
	}
	return out
}

func pluginRepoStatements(urls []string) []string {
	var out []string
	for _, u := range urls {
		switch u {
		case "https://plugins.gradle.org/maven2":
			out = append(out, "gradlePluginPortal()")
		case "https://dl.google.com/dl/android/maven2":
			out = append(out, "google()")
		case "https://repo.maven.apache.org/maven2":
			out = append(out, "mavenCentral()")
		case "mavenLocal":
			out = append(out, "mavenLocal()")
		default:
			out = append(out, fmt.Sprintf("maven { url = uri(%s) }", quoteKotlin(u)))
		}
	}
	return out
}

func generateVersionCatalog(c *VersionCatalog) string {
	var b strings.Builder
	b.WriteString("# 由 Canter (`canter convert --to-gradle`) 生成。\n\n")
	if len(c.Versions) > 0 {
		b.WriteString("[versions]\n")
		for _, k := range sortedKeys(c.Versions) {
			fmt.Fprintf(&b, "%s = %s\n", k, quoteKotlin(c.Versions[k]))
		}
		b.WriteString("\n")
	}
	if len(c.Libraries) > 0 {
		b.WriteString("[libraries]\n")
		for _, name := range sortedMapKeys(c.Libraries) {
			lib := c.Libraries[name]
			group := lib["group"]
			art := lib["name"]
			if lib["module"] != "" && (group == "" || art == "") {
				parts := strings.SplitN(lib["module"], ":", 2)
				if len(parts) == 2 {
					group, art = parts[0], parts[1]
				}
			}
			fmt.Fprintf(&b, "%s = { group = %s, name = %s", name, quoteKotlin(group), quoteKotlin(art))
			if ref := lib["version.ref"]; ref != "" {
				fmt.Fprintf(&b, ", version.ref = %s", quoteKotlin(ref))
			} else if v := lib["version"]; v != "" {
				fmt.Fprintf(&b, ", version = %s", quoteKotlin(v))
			}
			b.WriteString(" }\n")
		}
		b.WriteString("\n")
	}
	if len(c.Plugins) > 0 {
		b.WriteString("[plugins]\n")
		for _, name := range sortedMapKeys(c.Plugins) {
			pl := c.Plugins[name]
			fmt.Fprintf(&b, "%s = { id = %s", name, quoteKotlin(pl["id"]))
			if ref := pl["version.ref"]; ref != "" {
				fmt.Fprintf(&b, ", version.ref = %s", quoteKotlin(ref))
			} else if v := pl["version"]; v != "" {
				fmt.Fprintf(&b, ", version = %s", quoteKotlin(v))
			}
			b.WriteString(" }\n")
		}
		b.WriteString("\n")
	}
	if len(c.Bundles) > 0 {
		b.WriteString("[bundles]\n")
		for _, name := range sortedMapKeys(c.Bundles) {
			fmt.Fprintf(&b, "%s = [%s]\n", name, joinQuoted(c.Bundles[name]))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func generateBuildFile(mod ModuleConfig, catalog *VersionCatalog) string {
	var b strings.Builder
	b.WriteString("// 由 Canter (`canter convert --to-gradle`) 生成。\n")
	if len(mod.Plugins) > 0 {
		b.WriteString("plugins {\n")
		for _, p := range mod.Plugins {
			catalogName := pluginCatalogName(catalog, p)
			if catalogName != "" {
				fmt.Fprintf(&b, "    alias(libs.plugins.%s)\n", catalogName)
				continue
			}
			if v := mod.PluginVersions[p]; v != "" {
				fmt.Fprintf(&b, "    id(%s) version %s\n", quoteKotlin(p), quoteKotlin(v))
			} else {
				fmt.Fprintf(&b, "    id(%s)\n", quoteKotlin(p))
			}
		}
		b.WriteString("}\n\n")
	}
	if mod.Android != nil {
		writeAndroidBlock(&b, mod.Android)
	}
	if len(mod.BuildFeatures) > 0 || mod.Android != nil {
		writeBuildFeatures(&b, mod)
	}
	if len(mod.PackagingExcludes) > 0 {
		b.WriteString("\nandroid {\n    packaging {\n        resources {\n")
		for _, ex := range mod.PackagingExcludes {
			fmt.Fprintf(&b, "            excludes += %s\n", quoteKotlin(ex))
		}
		b.WriteString("        }\n    }\n}\n")
	}
	if writesAndroidComponents(mod.Android) {
		writeAndroidComponents(&b, mod.Android)
	}
	if len(mod.Dependencies) > 0 {
		b.WriteString("\ndependencies {\n")
		for _, dep := range mod.Dependencies {
			fmt.Fprintf(&b, "    %s\n", dependencyLine(dep, catalog))
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func writesAndroidComponents(a *AndroidConfig) bool {
	return a != nil && len(a.VariantConfigs) > 0
}

func writeBuildFeatures(b *strings.Builder, mod ModuleConfig) {
	features := map[string]bool{}
	for k, v := range mod.BuildFeatures {
		features[k] = v
	}
	if mod.Android != nil && mod.Android.ComposeEnabled {
		features["compose"] = true
	}
	if len(features) == 0 {
		return
	}
	b.WriteString("\nandroid {\n    buildFeatures {\n")
	for _, k := range sortedBoolKeys(features) {
		fmt.Fprintf(b, "        %s = %s\n", k, kotlinBool(features[k]))
	}
	b.WriteString("    }\n}\n")
}

func writeAndroidBlock(b *strings.Builder, a *AndroidConfig) {
	b.WriteString("android {\n")
	if a.Namespace != "" {
		fmt.Fprintf(b, "    namespace = %s\n", quoteKotlin(a.Namespace))
	}
	if a.CompileSDK != nil {
		if isIntegral(*a.CompileSDK) {
			fmt.Fprintf(b, "    compileSdk = %d\n", int64(*a.CompileSDK))
		} else {
			major := int64(*a.CompileSDK)
			minor := int((*a.CompileSDK - float64(major)) * 10)
			fmt.Fprintf(b, "    compileSdk {\n        version = release(%d) {\n            minorApiLevel = %d\n        }\n    }\n", major, minor)
		}
	}

	b.WriteString("\n    defaultConfig {\n")
	if a.ApplicationID != "" {
		fmt.Fprintf(b, "        applicationId = %s\n", quoteKotlin(a.ApplicationID))
	}
	if a.MinSDK != nil {
		fmt.Fprintf(b, "        minSdk = %d\n", *a.MinSDK)
	}
	if a.TargetSDK != nil {
		fmt.Fprintf(b, "        targetSdk = %d\n", *a.TargetSDK)
	}
	if a.VersionCode != nil {
		fmt.Fprintf(b, "        versionCode = %d\n", *a.VersionCode)
	}
	if a.VersionName != "" {
		fmt.Fprintf(b, "        versionName = %s\n", quoteKotlin(a.VersionName))
	}
	if a.TestInstrumentationRunner != "" {
		fmt.Fprintf(b, "        testInstrumentationRunner = %s\n", quoteKotlin(a.TestInstrumentationRunner))
	}
	b.WriteString("    }\n")

	if len(a.SigningConfigs) > 0 {
		b.WriteString("\n    signingConfigs {\n")
		for _, name := range sortedMapKeys(a.SigningConfigs) {
			s := a.SigningConfigs[name]
			fmt.Fprintf(b, "        create(%s) {\n", quoteKotlin(name))
			if s.StoreFile != "" {
				fmt.Fprintf(b, "            storeFile = file(%s)\n", quoteKotlin(exprToPath(s.StoreFile)))
			}
			if s.StorePassword != "" {
				fmt.Fprintf(b, "            storePassword = %s\n", valueExpr(s.StorePassword))
			}
			if s.KeyAlias != "" {
				fmt.Fprintf(b, "            keyAlias = %s\n", valueExpr(s.KeyAlias))
			}
			if s.KeyPassword != "" {
				fmt.Fprintf(b, "            keyPassword = %s\n", valueExpr(s.KeyPassword))
			}
			b.WriteString("        }\n")
		}
		b.WriteString("    }\n")
	}

	if len(a.Flavors) > 0 {
		b.WriteString("\n    productFlavors {\n")
		for _, name := range sortedMapKeys(a.Flavors) {
			f := a.Flavors[name]
			fmt.Fprintf(b, "        create(%s) {\n", quoteKotlin(name))
			if f.VersionNameSuffix != "" {
				fmt.Fprintf(b, "            versionNameSuffix = %s\n", quoteKotlin(f.VersionNameSuffix))
			}
			for _, bcf := range f.BuildConfigFields {
				fmt.Fprintf(b, "            %s\n", buildConfigFieldLine(bcf))
			}
			if len(f.ProguardFiles) > 0 {
				fmt.Fprintf(b, "            proguardFiles(%s)\n", joinRaw(f.ProguardFiles))
			}
			b.WriteString("        }\n")
		}
		b.WriteString("    }\n")
	}

	if a.MinifyEnabled || a.ShrinkResources || len(a.ProguardFiles) > 0 || a.SigningConfig != "" {
		b.WriteString("\n    buildTypes {\n        release {\n")
		if a.MinifyEnabled {
			b.WriteString("            isMinifyEnabled = true\n")
		}
		if a.ShrinkResources {
			b.WriteString("            isShrinkResources = true\n")
		}
		if len(a.ProguardFiles) > 0 {
			fmt.Fprintf(b, "            proguardFiles(%s)\n", joinRaw(a.ProguardFiles))
		}
		if a.SigningConfig != "" {
			fmt.Fprintf(b, "            signingConfig = signingConfigs.getByName(%s)\n", quoteKotlin(a.SigningConfig))
		}
		b.WriteString("        }\n    }\n")
	}

	if a.SourceCompatibility != "" || a.TargetCompatibility != "" {
		b.WriteString("\n    compileOptions {\n")
		if a.SourceCompatibility != "" {
			fmt.Fprintf(b, "        sourceCompatibility = %s\n", javaVersionExpr(a.SourceCompatibility))
		}
		if a.TargetCompatibility != "" {
			fmt.Fprintf(b, "        targetCompatibility = %s\n", javaVersionExpr(a.TargetCompatibility))
		}
		b.WriteString("    }\n")
	}
	if a.JvmTarget != "" {
		fmt.Fprintf(b, "\n    kotlinOptions {\n        jvmTarget = %s\n    }\n", quoteKotlin(a.JvmTarget))
	}
	if len(a.ABIFilters) > 0 {
		fmt.Fprintf(b, "\n    ndk {\n        abiFilters += listOf(%s)\n    }\n", joinQuoted(a.ABIFilters))
	}
	if a.SplitABIEnable || a.SplitABIUniversalDecl != nil || len(a.SplitABIInclude) > 0 {
		b.WriteString("\n    splits {\n        abi {\n")
		if a.SplitABIEnable {
			b.WriteString("            isEnable = true\n")
		}
		if a.SplitABIUniversalDecl != nil {
			fmt.Fprintf(b, "            isUniversalApk = %s\n", kotlinBool(*a.SplitABIUniversalDecl))
		}
		if len(a.SplitABIInclude) > 0 {
			fmt.Fprintf(b, "            include(%s)\n", joinQuoted(a.SplitABIInclude))
		}
		b.WriteString("        }\n    }\n")
	}
	b.WriteString("}\n")
}

func writeAndroidComponents(b *strings.Builder, a *AndroidConfig) {
	b.WriteString("\nandroidComponents {\n")
	for _, vc := range a.VariantConfigs {
		selector := "selector()"
		if vc.BuildType != "" {
			selector = fmt.Sprintf("selector().withBuildType(%s)", quoteKotlin(vc.BuildType))
		}
		fmt.Fprintf(b, "    onVariants(%s) { variant ->\n", selector)
		if len(vc.LocaleFilters) > 0 {
			fmt.Fprintf(b, "        variant.androidResources.localeFilters.addAll(%s)\n", joinQuoted(vc.LocaleFilters))
		}
		if vc.UseLegacyPackaging != nil {
			fmt.Fprintf(b, "        variant.packaging.jniLibs.useLegacyPackaging.set(%s)\n", kotlinBool(*vc.UseLegacyPackaging))
		}
		if len(vc.ResourceExcludes) > 0 {
			fmt.Fprintf(b, "        variant.packaging.resources.excludes.addAll(%s)\n", joinQuoted(vc.ResourceExcludes))
		}
		b.WriteString("    }\n")
	}
	b.WriteString("}\n")
}

// dependencyLine 生成一行依赖声明。优先使用版本目录别名。
func dependencyLine(dep Dependency, catalog *VersionCatalog) string {
	scope := dep.Scope
	if scope == "" {
		scope = "implementation"
	}
	if dep.IsProject {
		return fmt.Sprintf("%s(project(%s))", scope, quoteKotlin(dep.ProjectPath))
	}
	if catalog != nil {
		if accessor := catalogAccessor(catalog, dep.Group, dep.Artifact, dep.Version); accessor != "" {
			if dep.IsPlatform {
				return fmt.Sprintf("%s(platform(libs.%s))", scope, accessor)
			}
			return fmt.Sprintf("%s(libs.%s)", scope, accessor)
		}
	}
	coord := fmt.Sprintf("%s:%s:%s", dep.Group, dep.Artifact, dep.Version)
	if dep.IsPlatform {
		return fmt.Sprintf("%s(platform(%s))", scope, quoteKotlin(coord))
	}
	return fmt.Sprintf("%s(%s)", scope, quoteKotlin(coord))
}

// catalogAccessor 若依赖坐标命中版本目录条目，返回其访问器（点分）。
func catalogAccessor(c *VersionCatalog, group, artifact, version string) string {
	for name, lib := range c.Libraries {
		g := lib["group"]
		a := lib["name"]
		if lib["module"] != "" && (g == "" || a == "") {
			parts := strings.SplitN(lib["module"], ":", 2)
			if len(parts) == 2 {
				g, a = parts[0], parts[1]
			}
		}
		if g == group && a == artifact {
			return normalizeAccessor(name)
		}
	}
	return ""
}

// pluginCatalogName 若插件 id 命中版本目录条目，返回其访问器。
func pluginCatalogName(c *VersionCatalog, id string) string {
	if c == nil {
		return ""
	}
	for name, pl := range c.Plugins {
		if pl["id"] == id {
			return normalizeAccessor(name)
		}
	}
	return ""
}

func exprToPath(expr string) string {
	return strings.TrimPrefix(expr, "file:")
}

// valueExpr 把内部表达式（env:X / 字面量）还原为 Kotlin 表达式。
func valueExpr(expr string) string {
	if strings.HasPrefix(expr, "env:") {
		return fmt.Sprintf("System.getenv(%s)", quoteKotlin(strings.TrimPrefix(expr, "env:")))
	}
	return quoteKotlin(expr)
}

func buildConfigFieldLine(bcf string) string {
	parts := strings.SplitN(bcf, ":", 3)
	if len(parts) != 3 {
		return ""
	}
	return fmt.Sprintf("buildConfigField(%s, %s, %s)", quoteKotlin(parts[0]), quoteKotlin(parts[1]), quoteKotlin(parts[2]))
}

func javaVersionExpr(v string) string {
	v = strings.TrimPrefix(v, "VERSION_")
	v = strings.TrimPrefix(v, "1.")
	switch v {
	case "7", "8", "9", "10", "11", "12", "13", "14", "15", "16", "17", "18", "19", "20", "21":
		return "JavaVersion.VERSION_" + v
	}
	if strings.HasPrefix(v, "VERSION_") {
		return "JavaVersion." + v
	}
	return "JavaVersion.VERSION_" + v
}

func isIntegral(f float64) bool { return f == float64(int64(f)) }

func quoteKotlin(s string) string {
	return fmt.Sprintf("%q", s)
}

func kotlinBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func joinQuoted(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = quoteKotlin(s)
	}
	return strings.Join(parts, ", ")
}

// joinRaw 把 proguardFiles 的路径列表转为 Kotlin 实参（保留 file() 语义）。
func joinRaw(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = quoteKotlin(s)
	}
	return strings.Join(parts, ", ")
}
