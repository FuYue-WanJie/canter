package builder

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"canter/internal/engine"
	"canter/internal/mirror"
	"canter/internal/parser"
)

// Builder 构建引擎
type Builder struct {
	ProjectDir string
	Config     *parser.ProjectConfig
	BuildDir   string
	CacheDir   string
	Context    *engine.BuildContext
	MirrorMgr  *mirror.Manager
	Home       string
}

// NewBuilder 创建构建引擎
func NewBuilder(projectDir string, config *parser.ProjectConfig) *Builder {
	home, _ := os.UserHomeDir()
	buildDir := filepath.Join(projectDir, "build", "canter")
	cacheDir := filepath.Join(home, ".canter", "cache", filepath.Base(projectDir))
	sdk := os.Getenv("ANDROID_SDK_ROOT")
	if sdk == "" {
		sdk = filepath.Join(home, ".android-sdk")
	}
	compileSDK := "35"
	moduleDir := projectDir

	for _, mod := range config.Modules {
		if mod.Android != nil && mod.Android.CompileSDK != nil {
			compileSDK = formatCompileSDK(*mod.Android.CompileSDK)
		}
		if mod.Android != nil && mod.Android.ApplicationID != "" {
			if mod.Path != "" {
				moduleDir = mod.Path
			}
			break
		}
	}

	// platform 目录解析（候选：android-<sdk>、android-<sdk>.0、android-<major>.0、android-<major>）
	major := strings.SplitN(compileSDK, ".", 2)[0]
	platformDir := filepath.Join(sdk, "platforms", "android-"+compileSDK)
	if _, err := os.Stat(platformDir); err != nil {
		for _, cand := range []string{"android-" + compileSDK, "android-" + compileSDK + ".0", "android-" + major + ".0", "android-" + major} {
			if info, err := os.Stat(filepath.Join(sdk, "platforms", cand)); err == nil && info.IsDir() {
				platformDir = filepath.Join(sdk, "platforms", cand)
				break
			}
		}
	}

	// build-tools 版本推导
	btVersion := compileSDK + ".0.0"
	if strings.Contains(compileSDK, ".") {
		btVersion = compileSDK + ".0"
	}
	btDir := filepath.Join(sdk, "build-tools", btVersion)
	if _, err := os.Stat(btDir); err != nil {
		btRoot := filepath.Join(sdk, "build-tools")
		major := strings.SplitN(compileSDK, ".", 2)[0]
		if entries, err := os.ReadDir(btRoot); err == nil {
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			// 优先匹配 major 前缀，找不到则回退到最高可用版本
			var highest string
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				if e.Name() > highest {
					highest = e.Name()
				}
				if strings.HasPrefix(e.Name(), major) {
					btDir = filepath.Join(btRoot, e.Name())
					highest = ""
					break
				}
			}
			if highest != "" {
				btDir = filepath.Join(btRoot, highest)
				fmt.Printf("build-tools %s 不可用，回退到 %s\n", btVersion, highest)
			}
		}
	}

	appConfig := &AppConfig{
		Namespace:  "",
		MinSDK:     "",
		TargetSDK:  "",
		CompileSDK: compileSDK,
		ABIFilters: nil,
		ModuleName: filepath.Base(moduleDir),
		ModuleDir:  moduleDir,
	}
	for _, mod := range config.Modules {
		if mod.Android == nil {
			continue
		}
		appConfig.Namespace = mod.Android.Namespace
		appConfig.ApplicationID = mod.Android.ApplicationID
		if mod.Android.VersionCode != nil {
			appConfig.VersionCode = fmt.Sprintf("%d", *mod.Android.VersionCode)
		}
		appConfig.VersionName = mod.Android.VersionName
		if mod.Android.MinSDK != nil {
			appConfig.MinSDK = fmt.Sprintf("%d", *mod.Android.MinSDK)
		}
		if mod.Android.TargetSDK != nil {
			appConfig.TargetSDK = fmt.Sprintf("%d", *mod.Android.TargetSDK)
		}
		if len(mod.Android.ABIFilters) > 0 {
			appConfig.ABIFilters = mod.Android.ABIFilters
		}
		appConfig.MinifyEnabled = mod.Android.MinifyEnabled
		appConfig.ShrinkResources = mod.Android.ShrinkResources
		appConfig.ProguardFiles = mod.Android.ProguardFiles
		appConfig.JvmTarget = mod.Android.JvmTarget
		appConfig.BuildConfigs = mod.Android.BuildConfigFields
		appConfig.SelectedFlavor = mod.Android.SelectedFlavor
		appConfig.VersionNameSuffix = mod.Android.FlavorVersionNameSuffix
		appConfig.SplitABIEnable = mod.Android.SplitABIEnable
		appConfig.SigningConfigs = map[string]parser.SigningConfigEntry{}
		for name, sc := range mod.Android.SigningConfigs {
			appConfig.SigningConfigs[name] = sc
		}
		appConfig.SigningConfig = mod.Android.SigningConfig
		// splits.abi：universal APK 默认开启（Gradle 默认值），显式 false 关闭；
		// include(...) 的 ABI 列表缺省回退 ndk.abiFilters
		if mod.Android.SplitABIEnable {
			appConfig.SplitABIUniversal = mod.Android.SplitABIUniversalDecl == nil || *mod.Android.SplitABIUniversalDecl
			if len(mod.Android.SplitABIInclude) > 0 {
				appConfig.SplitABIInclude = mod.Android.SplitABIInclude
			} else if len(appConfig.ABIFilters) > 0 {
				appConfig.SplitABIInclude = appConfig.ABIFilters
			}
		}
		for _, pl := range mod.Plugins {
			if pl == "kotlin-parcelize" || pl == "org.jetbrains.kotlin.plugin.parcelize" {
				appConfig.Parcelize = true
			}
			if pl == "org.jetbrains.kotlin.plugin.compose" {
				appConfig.Compose = true
			}
			if pl == "org.jetbrains.kotlin.plugin.serialization" {
				appConfig.Serialization = true
			}
		}
		break
	}

	// 收集项目 library 模块的资源/assets/jniLibs，以及 app 自身（含选中 flavor）的 assets/jniLibs
	for _, mod := range config.Modules {
		if mod.Path == "" {
			continue
		}
		isApp := mod.Path == moduleDir
		sets := []string{"main"}
		if isApp && appConfig.SelectedFlavor != "" {
			sets = append(sets, appConfig.SelectedFlavor)
		}
		for _, set := range sets {
			base := filepath.Join(mod.Path, "src", set)
			if fi, err := os.Stat(filepath.Join(base, "res")); err == nil && fi.IsDir() {
				appConfig.LibraryResDirs = append(appConfig.LibraryResDirs, filepath.Join(base, "res"))
			}
			if fi, err := os.Stat(filepath.Join(base, "assets")); err == nil && fi.IsDir() {
				appConfig.LibraryAssets = append(appConfig.LibraryAssets, filepath.Join(base, "assets"))
			}
			if fi, err := os.Stat(filepath.Join(base, "jniLibs")); err == nil && fi.IsDir() {
				appConfig.LibraryJniLibs = append(appConfig.LibraryJniLibs, filepath.Join(base, "jniLibs"))
			}
		}
	}

	mirrorMgr := mirror.NewManager("")
	kotlinVersion := ""
	if config.Catalog != nil {
		kotlinVersion = config.Catalog.Versions["kotlin"]
	}

	ctx := &engine.BuildContext{
		ProjectDir:    projectDir,
		BuildDir:      buildDir,
		CacheDir:      cacheDir,
		AndroidSDK:    sdk,
		AndroidJar:    filepath.Join(platformDir, "android.jar"),
		BuildTools:    btDir,
		JavaHome:      os.Getenv("JAVA_HOME"),
		ModuleDir:     moduleDir,
		Repositories:  mirrorMgr.GetRepositories(),
		Config:        appConfig,
		StartTime:     time.Now(),
		ToolchainDir:  filepath.Join(home, ".canter", "toolchain"),
		KotlinVersion: kotlinVersion,
		NoGradleCache: os.Getenv("CANTER_NO_GRADLE_CACHE") != "",
	}

	return &Builder{
		ProjectDir: projectDir,
		Config:     config,
		BuildDir:   buildDir,
		CacheDir:   cacheDir,
		Context:    ctx,
		MirrorMgr:  mirrorMgr,
		Home:       home,
	}
}

func formatCompileSDK(sdk float64) string {
	s := fmt.Sprintf("%g", sdk)
	return s
}

// variantKey 返回当前构建变体标识（debug/release + 选中 flavor），用于隔离任务缓存
func (b *Builder) variantKey() string {
	if ac, ok := b.Context.Config.(*AppConfig); ok {
		kind := "debug"
		if ac.Release {
			kind = "release"
		}
		if ac.SelectedFlavor != "" {
			kind += "-" + ac.SelectedFlavor
		}
		return kind
	}
	return "debug"
}

// Assemble 执行完整构建（release 决定是否启用 R8 混淆路径）
func (b *Builder) Assemble(release bool) error {
	if cfg, ok := b.Context.Config.(*AppConfig); ok {
		cfg.Release = release
	}
	os.MkdirAll(b.BuildDir, 0755)
	os.MkdirAll(b.CacheDir, 0755)
	fmt.Printf("项目: %s, 模块: %s\n", b.Config.ProjectName, b.Context.ModuleDirOrProject())

	if err := b.resolveDependencies(); err != nil {
		fmt.Printf("依赖解析失败: %v\n", err)
	}

	// 多模块：先编译 project 依赖的 library 模块，其 classes 并入 app classpath
	CompileProjectLibraries(b.Context, b.Config)

	graph := engine.NewTaskGraph()
	tasks := []*engine.Task{
		MergeResourcesTask(b.Context),
		Aapt2CompileTask(b.Context),
		Aapt2LinkTask(b.Context),
		SourceGenTask(b.Context),
		JavaCompileTask(b.Context),
		KotlinCompileTask(b.Context),
	}
	useR8 := false
	if cfg, ok := b.Context.Config.(*AppConfig); ok && cfg.MinifyCompatible() {
		useR8 = true
		fmt.Println("release + minifyEnabled=true → 使用 R8 混淆")
	}
	// 类合并：D8 与 R8 两条路径共用
	mergeIdx := 6
	tasks = append(tasks, MergeClassesTask(b.Context))
	if useR8 {
		tasks = append(tasks, R8MinifyTask(b.Context))
	} else {
		tasks = append(tasks, DexBuildTask(b.Context))
	}
	tasks = append(tasks, PackageApkTask(b.Context), SignApkTask(b.Context))

	for _, t := range tasks {
		graph.AddTask(t)
	}
	// 依赖图（对齐 AGP 有向无环结构）
	tasks[1].AddDependency("mergeResources")
	tasks[2].AddDependency("aapt2Compile")
	tasks[4].AddDependency("aapt2Link") // compileJava 需要 R.java
	tasks[4].AddDependency("generateBuildConfig")
	tasks[5].AddDependency("compileJava") // Kotlin 依赖 Java 类
	tasks[mergeIdx].AddDependency("compileKotlin")
	// mergeClasses 之后的任务索引：r8/dex=7, package=8, sign=9
	dexIdx := mergeIdx + 1
	tasks[dexIdx].AddDependency("mergeClasses")
	tasks[dexIdx+1].AddDependency("aapt2Link")
	tasks[dexIdx+1].AddDependency(tasks[dexIdx].Name)
	if len(tasks) > dexIdx+2 {
		tasks[dexIdx+2].AddDependency("packageApk")
	}

	// 任务缓存按变体隔离：debug/release（及 flavor）的输出语义不同，
	// 复用彼此的任务缓存会导致切换变体时被误判为“已是最新”。
	variantCache := filepath.Join(b.CacheDir, b.variantKey())
	result := graph.Execute(b.Context, variantCache, 4)
	if !result.Success() {
		fmt.Println("BUILD FAILED")
		fmt.Printf("失败任务: %v\n", result.Failed)
		return fmt.Errorf("build failed")
	}
	fmt.Printf("BUILD SUCCESSFUL in %.1fs\n", b.Context.Elapsed().Seconds())
	fmt.Printf("执行: %d, 跳过: %d\n", len(result.Executed), len(result.Skipped))
	// 列出全部签名产物（universal + per-ABI）
	for _, v := range apkVariants(b.Context.Config.(*AppConfig)) {
		signed := filepath.Join(b.BuildDir, v.Name+"-signed.apk")
		if info, err := os.Stat(signed); err == nil {
			fmt.Printf("APK: %s (%dKB)\n", signed, info.Size()/1024)
		}
	}
	return nil
}

// Clean 清理构建产物
func (b *Builder) Clean() {
	for _, d := range []string{b.BuildDir, b.CacheDir} {
		if err := os.RemoveAll(d); err != nil {
			fmt.Printf("清理失败: %s: %v\n", d, err)
		} else {
			fmt.Printf("已清理: %s\n", d)
		}
	}
}

// depsSignatureFile 依赖解压标记文件（作为任务输入：内容随依赖集合变化，O(1) 廉价）
func depsSignatureFile(ctx *engine.BuildContext) string {
	return filepath.Join(ctx.BuildDir, "deps", ".canter-reskey")
}

// resolutionCacheFile 解析缓存的落盘路径（放在 clean 目录之外，clean 后仍可复用避免 BFS 重跑）
func (b *Builder) resolutionCacheFile() string {
	return b.CacheDir + ".resolution.json"
}

// resolveDependencies 依赖解析：全图 BFS + 版本冲突消解（GBL: group:artifact 取最高版本）
func (b *Builder) resolveDependencies() error {
	depsDir := filepath.Join(b.BuildDir, "deps")
	os.MkdirAll(depsDir, 0755)
	repos := b.MirrorMgr.GetRepositories()
	downloader := NewDownloader(repos, filepath.Join(b.Home, ".canter", "cache", "deps"))

	type coord struct {
		group, artifact, version string
	}

	// 收集直接依赖坐标（跳过 test/androidTest 作用域）
	var directDeps []coord
	for _, mod := range b.Config.Modules {
		for _, dep := range mod.Dependencies {
			if dep.IsProject || dep.Group == "" || dep.Artifact == "" {
				continue
			}
			if dep.Version == "" || dep.Version == "unknown" {
				continue
			}
			if strings.Contains(strings.ToLower(dep.Scope), "test") {
				continue
			}
			directDeps = append(directDeps, coord{dep.Group, dep.Artifact, normalizeVersion(dep.Version)})
		}
	}
	if kotlinVersion := b.Config.Catalog.Versions["kotlin"]; kotlinVersion != "" {
		directDeps = append(directDeps, coord{"org.jetbrains.kotlin", "kotlin-stdlib", kotlinVersion})
	}

	// 收集 BOM 约束（platform(...) 声明），对全图（含传递依赖）生效
	bomConstraints := map[string]string{} // key group:artifact -> 约束版本
	for _, mod := range b.Config.Modules {
		for _, dep := range mod.Dependencies {
			if !dep.IsPlatform || dep.Group == "" || dep.Version == "" {
				continue
			}
			if m := b.Config.Catalog.BomConstraints(dep.Group, dep.Version); m != nil {
				for k, v := range m {
					if cur, ok := bomConstraints[k]; !ok || compareVersions(v, cur) > 0 {
						bomConstraints[k] = v
					}
				}
			}
		}
	}
	if len(bomConstraints) > 0 {
		fmt.Printf("BOM 约束: %d 条\n", len(bomConstraints))
	}

	// 解析结果缓存：命中则跳过全图 BFS，直接按缓存坐标下载解压（大幅提速）
	directCoords := make([]depCoord, 0, len(directDeps))
	for _, d := range directDeps {
		directCoords = append(directCoords, depCoord{d.group, d.artifact, d.version})
	}
	kotlinVer := b.Config.Catalog.Versions["kotlin"]
	resKey := resolutionKey(directCoords, bomConstraints, kotlinVer)
	if cached, ok := loadResolutionCache(b.resolutionCacheFile(), resKey); ok {
		cached = collapseVariantCoords(cached)
		matKey := resKey + ":" + coordHash(cached)
		markerFile := filepath.Join(depsDir, ".canter-reskey")
		if data, err := os.ReadFile(markerFile); err == nil && string(data) == matKey {
			fmt.Printf("依赖解析: 缓存命中（%d 个依赖，已解压，跳过）\n", len(cached))
			return nil
		}
		fmt.Printf("依赖解析: 缓存命中（%d 个依赖）\n", len(cached))
		os.RemoveAll(depsDir)
		os.MkdirAll(depsDir, 0755)
		if err := b.materializeDeps(cached, depsDir, downloader); err != nil {
			return err
		}
		os.WriteFile(markerFile, []byte(matKey), 0644)
		return nil
	}

	// 全图 BFS：先只下载 POM 解析传递依赖，收集所有 (group, artifact, version) 候选
	// 版本冲突消解：对每个 group:artifact 记录所有出现过的版本，最终选最高
	reported := map[string]bool{} // key g:a:v 已解析过 POM
	type gaVers struct {
		group, artifact string
		versions        []string
	}
	gaMap := map[string]*gaVers{} // key g:a -> 版本候选集
	getGA := func(g, a string) *gaVers {
		key := g + ":" + a
		if v, ok := gaMap[key]; ok {
			return v
		}
		v := &gaVers{group: g, artifact: a}
		gaMap[key] = v
		return v
	}
	recordVersion := func(g, a, v string) {
		v = normalizeVersion(v)
		if v == "" {
			return
		}
		gv := getGA(g, a)
		for _, exist := range gv.versions {
			if exist == v {
				return
			}
		}
		gv.versions = append(gv.versions, v)
	}

	// 队列：先加入所有直接依赖
	queue := make([]coord, 0, len(directDeps))
	queue = append(queue, directDeps...)
	for _, d := range directDeps {
		recordVersion(d.group, d.artifact, d.version)
	}

	// BFS 解析（不限深度，visited 按 g:a:v 去重）
	for len(queue) > 0 {
		var nextQ []coord
		for _, d := range queue {
			key := d.group + ":" + d.artifact + ":" + d.version
			if reported[key] {
				continue
			}
			reported[key] = true
			// BFS 只需要 POM 用于图遍历（本地缓存优先 + 短超时，避免下载整个 artifact）
			if _, err := downloader.FetchPOM(d.group, d.artifact, d.version); err != nil {
				continue
			}
			// 若该依赖是 BOM（packaging=pom），把其 dependencyManagement 作为全局版本约束
			if isPlat, mgmt := b.pomIsPlatform(d.group, d.artifact, d.version, downloader); isPlat {
				for k, v := range mgmt {
					nv := normalizeVersion(v)
					if cur, ok := bomConstraints[k]; !ok || compareVersions(nv, cur) > 0 {
						bomConstraints[k] = nv
					}
				}
			}
			// 处理该 POM 的 dependencyManagement 中被 import 的 BOM，并合并其约束
			for _, imp := range b.pomBOMImports(d.group, d.artifact, d.version, downloader) {
				iv := normalizeVersion(imp.version)
				if _, err := downloader.FetchPOM(imp.group, imp.artifact, iv); err != nil {
					continue
				}
				if ok, m := b.pomIsPlatform(imp.group, imp.artifact, iv, downloader); ok {
					for k, v := range m {
						nv := normalizeVersion(v)
						if cur, ok2 := bomConstraints[k]; !ok2 || compareVersions(nv, cur) > 0 {
							bomConstraints[k] = nv
						}
					}
				}
			}
			// 从 POM 解析传递依赖
			for _, t := range b.parsePomDependencies(d.group, d.artifact, d.version, downloader) {
				normV := normalizeVersion(t.version)
				recordVersion(t.group, t.artifact, normV)
				nextQ = append(nextQ, coord{t.group, t.artifact, normV})
			}
			// 从 Gradle Module Metadata 解析依赖与约束（KMP 库真实依赖常仅在此）
			mdeps, mcons := b.parseModuleDeps(downloader, d.group, d.artifact, d.version)
			for _, t := range mdeps {
				normV := normalizeVersion(t.version)
				recordVersion(t.group, t.artifact, normV)
				nextQ = append(nextQ, coord{t.group, t.artifact, normV})
			}
			for k, v := range mcons {
				nv := normalizeVersion(v)
				if cur, ok := bomConstraints[k]; !ok || compareVersions(nv, cur) > 0 {
					bomConstraints[k] = nv
				}
			}
		}
		queue = nextQ
	}

	// 版本冲突消解：每个 group:artifact 取最高版本
	// 得到一个消解后的最终坐标集合（保持直接依赖优先，但取最高版本）
	type resolvedDep struct {
		g, a, v string
	}
	selected := map[string]resolvedDep{} // key g:a
	maxVisible := map[string]string{}    // key g:a -> 最高可见版本

	// 先记录所有出现版本的最高值（对整个依赖图），并应用 BOM 约束
	for _, gv := range gaMap {
		key := gv.group + ":" + gv.artifact
		highest := selectHighestVersion(gv.versions)
		// BOM/模块约束可能声明在 base 名上（如 foundation），需同时按变体 base 名匹配
		baseKey := gv.group + ":" + variantBase(gv.artifact)
		if bc, ok := bomConstraints[key]; ok && bc != "" {
			if highest == "" || compareVersions(bc, highest) > 0 {
				highest = bc
			}
		}
		if baseKey != key {
			if bc, ok := bomConstraints[baseKey]; ok && bc != "" {
				if highest == "" || compareVersions(bc, highest) > 0 {
					highest = bc
				}
			}
		}
		maxVisible[key] = highest
	}

	// 收集最终解析集：直接依赖 + 它们传递的闭包都会出现在 gaMap 中
	for _, gv := range gaMap {
		key := gv.group + ":" + gv.artifact
		maxV := maxVisible[key]
		if maxV == "" {
			continue
		}
		selected[key] = resolvedDep{gv.group, gv.artifact, maxV}
	}

	// 收集最终坐标 → 写缓存 → 下载解压
	var selectedCoords []depCoord
	for _, sd := range selected {
		selectedCoords = append(selectedCoords, depCoord{sd.g, sd.a, sd.v})
	}
	selectedCoords = collapseVariantCoords(selectedCoords)
	saveResolutionCache(b.resolutionCacheFile(), resKey, selectedCoords)
	os.RemoveAll(depsDir)
	os.MkdirAll(depsDir, 0755)
	if err := b.materializeDeps(selectedCoords, depsDir, downloader); err != nil {
		return err
	}
	matKey := resKey + ":" + coordHash(selectedCoords)
	os.WriteFile(filepath.Join(depsDir, ".canter-reskey"), []byte(matKey), 0644)
	return nil
}

type pomDepEntry struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
	Type       string `xml:"type"`
	Optional   string `xml:"optional"`
}

type pomDepsModel struct {
	XMLName      xml.Name      `xml:"project"`
	Packaging    string        `xml:"packaging"`
	Dependencies []pomDepEntry `xml:"dependencies>dependency"`
	DepMgmt      []pomDepEntry `xml:"dependencyManagement>dependencies>dependency"`
}

// pomBOMImports 解析 POM 的 dependencyManagement 中被 import 的 BOM（type=pom, scope=import）
func (b *Builder) pomBOMImports(group, artifact, version string, downloader *Downloader) []struct{ group, artifact, version string } {
	groupPath := strings.ReplaceAll(group, ".", "/")
	pomPath := filepath.Join(downloader.CacheDir, filepath.FromSlash(groupPath), artifact, version, artifact+"-"+version+".pom")
	data, err := os.ReadFile(pomPath)
	if err != nil {
		return nil
	}
	var model pomDepsModel
	if err := xml.Unmarshal(data, &model); err != nil {
		return nil
	}
	var result []struct{ group, artifact, version string }
	for _, dm := range model.DepMgmt {
		if strings.EqualFold(strings.TrimSpace(dm.Type), "pom") && strings.EqualFold(strings.TrimSpace(dm.Scope), "import") {
			if dm.GroupID != "" && dm.ArtifactID != "" && dm.Version != "" {
				result = append(result, struct{ group, artifact, version string }{dm.GroupID, dm.ArtifactID, dm.Version})
			}
		}
	}
	return result
}

// pomIsPlatform 读取已缓存 POM，判断是否为 BOM（packaging=pom）并返回其 dependencyManagement 约束
func (b *Builder) pomIsPlatform(group, artifact, version string, downloader *Downloader) (bool, map[string]string) {
	groupPath := strings.ReplaceAll(group, ".", "/")
	pomPath := filepath.Join(downloader.CacheDir, filepath.FromSlash(groupPath), artifact, version, artifact+"-"+version+".pom")
	data, err := os.ReadFile(pomPath)
	if err != nil {
		return false, nil
	}
	var model pomDepsModel
	if err := xml.Unmarshal(data, &model); err != nil {
		return false, nil
	}
	if strings.TrimSpace(model.Packaging) != "pom" {
		return false, nil
	}
	mgmt := map[string]string{}
	for _, dm := range model.DepMgmt {
		if dm.GroupID != "" && dm.ArtifactID != "" && dm.Version != "" {
			mgmt[dm.GroupID+":"+dm.ArtifactID] = dm.Version
		}
	}
	return true, mgmt
}

// parsePomDependencies 解析 POM 的传递依赖
func (b *Builder) parsePomDependencies(group, artifact, version string, downloader *Downloader) []struct{ group, artifact, version string } {
	groupPath := strings.ReplaceAll(group, ".", "/")
	pomPath := filepath.Join(downloader.CacheDir, filepath.FromSlash(groupPath), artifact, version, artifact+"-"+version+".pom")
	data, err := os.ReadFile(pomPath)
	if err != nil {
		return nil
	}
	var model pomDepsModel
	if err := xml.Unmarshal(data, &model); err != nil {
		return nil
	}
	var result []struct{ group, artifact, version string }
	for _, dep := range model.Dependencies {
		if dep.GroupID == "" || dep.ArtifactID == "" {
			continue
		}
		if strings.TrimSpace(strings.ToLower(dep.Optional)) == "true" {
			continue
		}
		scope := dep.Scope
		if scope != "compile" && scope != "runtime" && scope != "import" && scope != "" {
			continue
		}
		if hasSuffix(dep.ArtifactID, nonAndroidSuffixes) {
			continue
		}
		v := dep.Version
		if v == "" {
			for _, dm := range model.DepMgmt {
				if dm.GroupID == dep.GroupID && dm.ArtifactID == dep.ArtifactID && dm.Version != "" {
					v = dm.Version
					break
				}
			}
		}
		if v != "" {
			result = append(result, struct{ group, artifact, version string }{dep.GroupID, dep.ArtifactID, v})
		}
	}
	return result
}
