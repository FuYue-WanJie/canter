package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CanterFileName 是 Canter 原生配置的文件名（项目根目录）。
const CanterFileName = "canter.toml"

// canterConfig 是 canter.toml 的字段级模型。零值表示未声明。
type canterConfig struct {
	Project   canterProject
	Props     map[string]string
	Versions  map[string]string
	Libraries map[string]canterLibrary
	Plugins   map[string]canterPlugin
	Bundles   map[string][]string
	Modules   []canterModule
}

type canterProject struct {
	Name               string
	Repositories       []string
	PluginRepositories []string
}

type canterLibrary struct {
	Group      string
	Name       string
	Version    string
	VersionRef string
}

type canterPlugin struct {
	ID         string
	Version    string
	VersionRef string
}

type canterModule struct {
	Name              string
	Path              string
	IncludeBuild      string
	Plugins           []string
	PluginVersions    map[string]string
	Android           *canterAndroid
	Dependencies      []canterDependency
	BuildFeatures     map[string]bool
	PackagingExcludes []string
}

type canterAndroid struct {
	Namespace                 string
	CompileSDK                *float64
	ApplicationID             string
	MinSDK                    *int
	TargetSDK                 *int
	VersionCode               *int
	VersionName               string
	TestInstrumentationRunner string
	JvmTarget                 string
	ComposeEnabled            bool
	MinifyEnabled             bool
	ShrinkResources           bool
	ProguardFiles             []string
	ABIFilters                []string
	SourceCompatibility       string
	TargetCompatibility       string
	SigningConfig             string
	BuildConfigFields         []string

	SelectedFlavor          string
	FlavorVersionNameSuffix string

	SplitABIEnable        bool
	SplitABIUniversalDecl *bool
	SplitABIInclude       []string

	SigningConfigs map[string]canterSigning

	Flavors        map[string]canterFlavor
	VariantConfigs []canterVariant
}

type canterSigning struct {
	StoreFile     string
	StorePassword string
	KeyAlias      string
	KeyPassword   string
}

type canterFlavor struct {
	VersionNameSuffix string
	BuildConfigFields []string
	ProguardFiles     []string
}

type canterVariant struct {
	BuildType          string
	LocaleFilters      []string
	UseLegacyPackaging *bool
	ResourceExcludes   []string
}

type canterDependency struct {
	Scope       string
	Group       string
	Artifact    string
	Version     string
	Library     string
	IsPlatform  bool
	IsProject   bool
	ProjectPath string
}

// ParseCanterConfig 读取 canter.toml 并转换为 ProjectConfig。
// 当文件不存在时返回 (nil, false)。
func ParseCanterConfig(projectDir string) (*ProjectConfig, bool) {
	path := filepath.Join(projectDir, CanterFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	doc := parseTOML(string(data))
	cc := decodeCanterConfig(doc)
	return toProjectConfig(projectDir, cc), true
}

// decodeCanterConfig 将通用 TOML 文档映射为 canterConfig。
func decodeCanterConfig(doc *tomlDoc) canterConfig {
	cc := canterConfig{
		Props:     map[string]string{},
		Versions:  map[string]string{},
		Libraries: map[string]canterLibrary{},
		Plugins:   map[string]canterPlugin{},
		Bundles:   map[string][]string{},
	}
	root := doc.root
	projTable := root.getTable("project")
	cc.Project.Name = projTable.getString("name")
	cc.Project.Repositories = projTable.getStringSlice("repositories")
	cc.Project.PluginRepositories = projTable.getStringSlice("pluginRepositories")

	for k, v := range tableFlat(root, "properties") {
		cc.Props[k] = v
	}
	for k, v := range tableFlat(root, "versions") {
		cc.Versions[k] = v
	}
	for name, t := range subtables(root, "libraries") {
		cc.Libraries[name] = canterLibrary{
			Group:      t.getString("group"),
			Name:       t.getString("name"),
			Version:    t.getString("version"),
			VersionRef: t.getString("version.ref"),
		}
	}
	for name, t := range subtables(root, "plugins") {
		cc.Plugins[name] = canterPlugin{
			ID:         t.getString("id"),
			Version:    t.getString("version"),
			VersionRef: t.getString("version.ref"),
		}
	}
	for name, arr := range subtables(root, "bundles") {
		cc.Bundles[name] = arr.getStringSlice("items")
	}
	for _, mt := range arrayTables(root, "modules") {
		cc.Modules = append(cc.Modules, decodeModule(mt))
	}
	return cc
}

func decodeModule(t *tomlTable) canterModule {
	m := canterModule{
		Name:           t.getString("name"),
		Path:           t.getString("path"),
		IncludeBuild:   t.getString("includeBuild"),
		Plugins:        t.getStringSlice("plugins"),
		PluginVersions: map[string]string{},
		BuildFeatures:  map[string]bool{},
	}
	for k, v := range tableFlat(t, "pluginVersions") {
		m.PluginVersions[k] = v
	}
	for k, v := range tableFlat(t, "buildFeatures") {
		m.BuildFeatures[k] = v == "true"
	}
	m.PackagingExcludes = t.getStringSlice("packagingExcludes")
	for _, at := range arrayTables(t, "dependencies") {
		m.Dependencies = append(m.Dependencies, canterDependency{
			Scope:       at.getString("scope"),
			Group:       at.getString("group"),
			Artifact:    at.getString("artifact"),
			Version:     at.getString("version"),
			Library:     at.getString("library"),
			IsPlatform:  at.getBool("platform"),
			IsProject:   at.getBool("project"),
			ProjectPath: at.getString("path"),
		})
	}
	if a := t.getTable("android"); a != nil {
		m.Android = decodeAndroid(a)
	}
	return m
}

func decodeAndroid(a *tomlTable) *canterAndroid {
	an := &canterAndroid{
		Namespace:                 a.getString("namespace"),
		ApplicationID:             a.getString("applicationId"),
		VersionName:               a.getString("versionName"),
		TestInstrumentationRunner: a.getString("testInstrumentationRunner"),
		JvmTarget:                 a.getString("jvmTarget"),
		ComposeEnabled:            a.getBool("composeEnabled"),
		MinifyEnabled:             a.getBool("minifyEnabled"),
		ShrinkResources:           a.getBool("shrinkResources"),
		ProguardFiles:             a.getStringSlice("proguardFiles"),
		ABIFilters:                a.getStringSlice("abiFilters"),
		SourceCompatibility:       a.getString("sourceCompatibility"),
		TargetCompatibility:       a.getString("targetCompatibility"),
		SigningConfig:             a.getString("signingConfig"),
		BuildConfigFields:         a.getStringSlice("buildConfigFields"),
		SelectedFlavor:            a.getString("selectedFlavor"),
		FlavorVersionNameSuffix:   a.getString("flavorVersionNameSuffix"),
		SplitABIEnable:            a.getBool("splitAbiEnable"),
		SplitABIInclude:           a.getStringSlice("splitAbiInclude"),
	}
	if v, ok := a.getFloat("compileSdk"); ok {
		an.CompileSDK = &v
	}
	if v, ok := a.getInt("minSdk"); ok {
		an.MinSDK = &v
	}
	if v, ok := a.getInt("targetSdk"); ok {
		an.TargetSDK = &v
	}
	if v, ok := a.getInt("versionCode"); ok {
		an.VersionCode = &v
	}
	if v, ok := a.getBoolPtr("splitAbiUniversalApk"); ok {
		an.SplitABIUniversalDecl = &v
	}

	for name, t := range subtables(a, "signingConfigs") {
		if an.SigningConfigs == nil {
			an.SigningConfigs = map[string]canterSigning{}
		}
		an.SigningConfigs[name] = canterSigning{
			StoreFile:     t.getString("storeFile"),
			StorePassword: t.getString("storePassword"),
			KeyAlias:      t.getString("keyAlias"),
			KeyPassword:   t.getString("keyPassword"),
		}
	}
	for name, t := range subtables(a, "flavors") {
		if an.Flavors == nil {
			an.Flavors = map[string]canterFlavor{}
		}
		an.Flavors[name] = canterFlavor{
			VersionNameSuffix: t.getString("versionNameSuffix"),
			BuildConfigFields: t.getStringSlice("buildConfigFields"),
			ProguardFiles:     t.getStringSlice("proguardFiles"),
		}
	}
	for _, vt := range arrayTables(a, "variants") {
		vc := canterVariant{
			BuildType:        vt.getString("buildType"),
			LocaleFilters:    vt.getStringSlice("localeFilters"),
			ResourceExcludes: vt.getStringSlice("resourceExcludes"),
		}
		if v, ok := vt.getBoolPtr("useLegacyPackaging"); ok {
			vc.UseLegacyPackaging = &v
		}
		an.VariantConfigs = append(an.VariantConfigs, vc)
	}
	return an
}

// toProjectConfig 将 canterConfig 转换为内部 ProjectConfig。
func toProjectConfig(projectDir string, cc canterConfig) *ProjectConfig {
	config := &ProjectConfig{
		RootDir:          projectDir,
		ProjectName:      cc.Project.Name,
		Catalog:          NewVersionCatalog(),
		GradleProperties: map[string]string{},
	}
	if config.ProjectName == "" {
		config.ProjectName = filepath.Base(projectDir)
	}
	for k, v := range cc.Props {
		config.GradleProperties[k] = v
	}
	for k, v := range cc.Versions {
		config.Catalog.Versions[k] = v
	}
	for name, lib := range cc.Libraries {
		entry := map[string]string{}
		if lib.Group != "" {
			entry["group"] = lib.Group
		}
		if lib.Name != "" {
			entry["name"] = lib.Name
		}
		if lib.Version != "" {
			entry["version"] = lib.Version
		}
		if lib.VersionRef != "" {
			entry["version.ref"] = lib.VersionRef
		}
		config.Catalog.Libraries[name] = entry
	}
	for name, pl := range cc.Plugins {
		entry := map[string]string{}
		if pl.ID != "" {
			entry["id"] = pl.ID
		}
		if pl.Version != "" {
			entry["version"] = pl.Version
		}
		if pl.VersionRef != "" {
			entry["version.ref"] = pl.VersionRef
		}
		config.Catalog.Plugins[name] = entry
	}
	for name, items := range cc.Bundles {
		config.Catalog.Bundles[name] = items
	}

	config.Repositories = resolveRepositories(cc.Project.Repositories,
		[]string{"https://dl.google.com/dl/android/maven2", "https://repo.maven.apache.org/maven2"})
	config.PluginRepositories = resolveRepositories(cc.Project.PluginRepositories,
		[]string{"https://plugins.gradle.org/maven2", "https://dl.google.com/dl/android/maven2", "https://repo.maven.apache.org/maven2"})

	for _, cm := range cc.Modules {
		module := ModuleConfig{
			Name:           cm.Name,
			Path:           cm.Path,
			IncludeBuild:   cm.IncludeBuild,
			Plugins:        cm.Plugins,
			PluginVersions: map[string]string{},
			BuildFeatures:  map[string]bool{},
		}
		if module.Name == "" && module.Path != "" {
			module.Name = filepath.Base(module.Path)
		}
		if module.Path == "" && module.Name != "" {
			module.Path = filepath.Join(projectDir, module.Name)
		} else if module.Path != "" && !filepath.IsAbs(module.Path) {
			module.Path = filepath.Join(projectDir, module.Path)
		}
		for k, v := range cm.PluginVersions {
			module.PluginVersions[k] = v
		}
		for k, v := range cm.BuildFeatures {
			module.BuildFeatures[k] = v
		}
		module.PackagingExcludes = cm.PackagingExcludes

		for _, cd := range cm.Dependencies {
			module.Dependencies = append(module.Dependencies, resolveDependency(cd, config.Catalog))
		}
		if cm.Android != nil {
			module.Android = toAndroidConfig(cm.Android)
		}
		config.Modules = append(config.Modules, module)
	}
	return config
}

func toAndroidConfig(ca *canterAndroid) *AndroidConfig {
	a := &AndroidConfig{
		Namespace:                 ca.Namespace,
		CompileSDK:                ca.CompileSDK,
		ApplicationID:             ca.ApplicationID,
		MinSDK:                    ca.MinSDK,
		TargetSDK:                 ca.TargetSDK,
		VersionCode:               ca.VersionCode,
		VersionName:               ca.VersionName,
		TestInstrumentationRunner: ca.TestInstrumentationRunner,
		JvmTarget:                 ca.JvmTarget,
		ComposeEnabled:            ca.ComposeEnabled,
		MinifyEnabled:             ca.MinifyEnabled,
		ShrinkResources:           ca.ShrinkResources,
		ProguardFiles:             ca.ProguardFiles,
		ABIFilters:                ca.ABIFilters,
		SourceCompatibility:       ca.SourceCompatibility,
		TargetCompatibility:       ca.TargetCompatibility,
		SigningConfig:             ca.SigningConfig,
		BuildConfigFields:         ca.BuildConfigFields,
		SelectedFlavor:            ca.SelectedFlavor,
		FlavorVersionNameSuffix:   ca.FlavorVersionNameSuffix,
		SplitABIEnable:            ca.SplitABIEnable,
		SplitABIUniversalDecl:     ca.SplitABIUniversalDecl,
		SplitABIInclude:           ca.SplitABIInclude,
	}
	if ca.SigningConfigs != nil {
		a.SigningConfigs = map[string]SigningConfigEntry{}
		for k, v := range ca.SigningConfigs {
			a.SigningConfigs[k] = SigningConfigEntry{
				StoreFile:     v.StoreFile,
				StorePassword: v.StorePassword,
				KeyAlias:      v.KeyAlias,
				KeyPassword:   v.KeyPassword,
			}
		}
	}
	if ca.Flavors != nil {
		a.Flavors = map[string]FlavorConfig{}
		for k, v := range ca.Flavors {
			a.Flavors[k] = FlavorConfig{
				VersionNameSuffix: v.VersionNameSuffix,
				BuildConfigFields: v.BuildConfigFields,
				ProguardFiles:     v.ProguardFiles,
			}
		}
	}
	for _, v := range ca.VariantConfigs {
		a.VariantConfigs = append(a.VariantConfigs, VariantConfig{
			BuildType:          v.BuildType,
			LocaleFilters:      v.LocaleFilters,
			UseLegacyPackaging: v.UseLegacyPackaging,
			ResourceExcludes:   v.ResourceExcludes,
		})
	}
	return a
}

// resolveDependency 将 Canter 依赖条目解析为内部 Dependency。
func resolveDependency(cd canterDependency, catalog *VersionCatalog) Dependency {
	dep := Dependency{
		Scope:       cd.Scope,
		IsPlatform:  cd.IsPlatform,
		IsProject:   cd.IsProject,
		ProjectPath: cd.ProjectPath,
	}
	if cd.Library != "" {
		g, a, v, ok := catalog.ResolveLibrary(cd.Library)
		if ok {
			dep.Group, dep.Artifact, dep.Version = g, a, v
			return dep
		}
	}
	dep.Group, dep.Artifact, dep.Version = cd.Group, cd.Artifact, cd.Version
	return dep
}

// resolveRepositories 把 google/mavenCentral 等简称归一化为 URL，保留完整 URL。
func resolveRepositories(names, defaults []string) []string {
	if len(names) == 0 {
		return defaults
	}
	var out []string
	for _, n := range names {
		switch n {
		case "google":
			out = append(out, "https://dl.google.com/dl/android/maven2")
		case "mavenCentral":
			out = append(out, "https://repo.maven.apache.org/maven2")
		case "mavenLocal":
			out = append(out, "mavenLocal")
		case "gradlePluginPortal":
			out = append(out, "https://plugins.gradle.org/maven2")
		default:
			out = append(out, n)
		}
	}
	return out
}

// repoShortName 把已知 URL 反向归一化为简称，未知 URL 原样保留。
func repoShortName(url string) string {
	switch url {
	case "https://dl.google.com/dl/android/maven2":
		return "google"
	case "https://repo.maven.apache.org/maven2":
		return "mavenCentral"
	case "mavenLocal":
		return "mavenLocal"
	case "https://plugins.gradle.org/maven2":
		return "gradlePluginPortal"
	}
	return url
}

// ---- TOML 写出：ProjectConfig -> canter.toml ----

// WriteCanterConfig 把 ProjectConfig 序列化为 canter.toml 文本。
func WriteCanterConfig(config *ProjectConfig) string {
	var b strings.Builder
	writeProjectSection(&b, config)
	writeFlatSection(&b, "properties", config.GradleProperties)
	if config.Catalog != nil {
		writeFlatSection(&b, "versions", config.Catalog.Versions)
		writeLibraries(&b, config.Catalog)
		writePlugins(&b, config.Catalog)
		writeBundles(&b, config.Catalog)
	}
	for _, mod := range config.Modules {
		writeModule(&b, mod, config.RootDir)
	}
	return b.String()
}

func writeProjectSection(b *strings.Builder, config *ProjectConfig) {
	b.WriteString("# Canter 原生配置。可由 `canter convert --to-canter <project>` 从 Gradle 生成，\n")
	b.WriteString("# 也可由 `canter convert --to-gradle <project>` 反向生成 Gradle 配置。\n\n")
	b.WriteString("[project]\n")
	fmt.Fprintf(b, "name = %s\n", tomlString(config.ProjectName))
	if repos := shortRepoNames(config.Repositories); len(repos) > 0 {
		fmt.Fprintf(b, "repositories = %s\n", tomlStringArray(repos))
	}
	if repos := shortRepoNames(config.PluginRepositories); len(repos) > 0 {
		fmt.Fprintf(b, "pluginRepositories = %s\n", tomlStringArray(repos))
	}
	b.WriteString("\n")
}

func shortRepoNames(urls []string) []string {
	var out []string
	for _, u := range urls {
		out = append(out, repoShortName(u))
	}
	return out
}

func writeFlatSection(b *strings.Builder, name string, m map[string]string) {
	if len(m) == 0 {
		return
	}
	fmt.Fprintf(b, "[%s]\n", name)
	keys := sortedKeys(m)
	for _, k := range keys {
		fmt.Fprintf(b, "%s = %s\n", tomlKey(k), tomlString(m[k]))
	}
	b.WriteString("\n")
}

func writeLibraries(b *strings.Builder, catalog *VersionCatalog) {
	if len(catalog.Libraries) == 0 {
		return
	}
	b.WriteString("[libraries]\n")
	for _, name := range sortedMapKeys(catalog.Libraries) {
		lib := catalog.Libraries[name]
		group := lib["group"]
		art := lib["name"]
		if lib["module"] != "" && (group == "" || art == "") {
			parts := strings.SplitN(lib["module"], ":", 2)
			if len(parts) == 2 {
				group, art = parts[0], parts[1]
			}
		}
		fmt.Fprintf(b, "%s = { group = %s, name = %s", tomlKey(name), tomlString(group), tomlString(art))
		if ref := lib["version.ref"]; ref != "" {
			fmt.Fprintf(b, ", version.ref = %s", tomlString(ref))
		} else if v := lib["version"]; v != "" {
			fmt.Fprintf(b, ", version = %s", tomlString(v))
		}
		b.WriteString(" }\n")
	}
	b.WriteString("\n")
}

func writePlugins(b *strings.Builder, catalog *VersionCatalog) {
	if len(catalog.Plugins) == 0 {
		return
	}
	b.WriteString("[plugins]\n")
	for _, name := range sortedMapKeys(catalog.Plugins) {
		pl := catalog.Plugins[name]
		fmt.Fprintf(b, "%s = { id = %s", tomlKey(name), tomlString(pl["id"]))
		if ref := pl["version.ref"]; ref != "" {
			fmt.Fprintf(b, ", version.ref = %s", tomlString(ref))
		} else if v := pl["version"]; v != "" {
			fmt.Fprintf(b, ", version = %s", tomlString(v))
		}
		b.WriteString(" }\n")
	}
	b.WriteString("\n")
}

func writeBundles(b *strings.Builder, catalog *VersionCatalog) {
	if len(catalog.Bundles) == 0 {
		return
	}
	b.WriteString("[bundles]\n")
	for _, name := range sortedMapKeys(catalog.Bundles) {
		fmt.Fprintf(b, "%s = { items = %s }\n", tomlKey(name), tomlStringArray(catalog.Bundles[name]))
	}
	b.WriteString("\n")
}

func writeModule(b *strings.Builder, mod ModuleConfig, rootDir string) {
	rel := modulePathRelative(rootDir, mod.Path)
	b.WriteString("[[modules]]\n")
	fmt.Fprintf(b, "name = %s\n", tomlString(mod.Name))
	if rel != "" {
		fmt.Fprintf(b, "path = %s\n", tomlString(rel))
	}
	if mod.IncludeBuild != "" {
		fmt.Fprintf(b, "includeBuild = %s\n", tomlString(mod.IncludeBuild))
	}
	if len(mod.Plugins) > 0 {
		fmt.Fprintf(b, "plugins = %s\n", tomlStringArray(mod.Plugins))
	}
	if len(mod.PackagingExcludes) > 0 {
		fmt.Fprintf(b, "packagingExcludes = %s\n", tomlStringArray(mod.PackagingExcludes))
	}
	if len(mod.PluginVersions) > 0 {
		b.WriteString("\n[modules.pluginVersions]\n")
		for _, k := range sortedKeys(mod.PluginVersions) {
			fmt.Fprintf(b, "%s = %s\n", tomlKey(k), tomlString(mod.PluginVersions[k]))
		}
	}
	if len(mod.BuildFeatures) > 0 {
		b.WriteString("\n[modules.buildFeatures]\n")
		for _, k := range sortedBoolKeys(mod.BuildFeatures) {
			fmt.Fprintf(b, "%s = %s\n", tomlKey(k), tomlBool(mod.BuildFeatures[k]))
		}
	}
	if mod.Android != nil {
		writeAndroid(b, mod.Android)
	}
	for _, dep := range mod.Dependencies {
		writeDependency(b, dep)
	}
	b.WriteString("\n")
}

func writeAndroid(b *strings.Builder, a *AndroidConfig) {
	b.WriteString("\n[modules.android]\n")
	if a.Namespace != "" {
		fmt.Fprintf(b, "namespace = %s\n", tomlString(a.Namespace))
	}
	if a.CompileSDK != nil {
		fmt.Fprintf(b, "compileSdk = %s\n", tomlFloat(*a.CompileSDK))
	}
	if a.ApplicationID != "" {
		fmt.Fprintf(b, "applicationId = %s\n", tomlString(a.ApplicationID))
	}
	if a.MinSDK != nil {
		fmt.Fprintf(b, "minSdk = %d\n", *a.MinSDK)
	}
	if a.TargetSDK != nil {
		fmt.Fprintf(b, "targetSdk = %d\n", *a.TargetSDK)
	}
	if a.VersionCode != nil {
		fmt.Fprintf(b, "versionCode = %d\n", *a.VersionCode)
	}
	if a.VersionName != "" {
		fmt.Fprintf(b, "versionName = %s\n", tomlString(a.VersionName))
	}
	if a.TestInstrumentationRunner != "" {
		fmt.Fprintf(b, "testInstrumentationRunner = %s\n", tomlString(a.TestInstrumentationRunner))
	}
	if a.JvmTarget != "" {
		fmt.Fprintf(b, "jvmTarget = %s\n", tomlString(a.JvmTarget))
	}
	if a.ComposeEnabled {
		fmt.Fprintf(b, "composeEnabled = %s\n", tomlBool(a.ComposeEnabled))
	}
	fmt.Fprintf(b, "minifyEnabled = %s\n", tomlBool(a.MinifyEnabled))
	fmt.Fprintf(b, "shrinkResources = %s\n", tomlBool(a.ShrinkResources))
	if len(a.ProguardFiles) > 0 {
		fmt.Fprintf(b, "proguardFiles = %s\n", tomlStringArray(a.ProguardFiles))
	}
	if len(a.ABIFilters) > 0 {
		fmt.Fprintf(b, "abiFilters = %s\n", tomlStringArray(a.ABIFilters))
	}
	if a.SourceCompatibility != "" {
		fmt.Fprintf(b, "sourceCompatibility = %s\n", tomlString(a.SourceCompatibility))
	}
	if a.TargetCompatibility != "" {
		fmt.Fprintf(b, "targetCompatibility = %s\n", tomlString(a.TargetCompatibility))
	}
	if a.SigningConfig != "" {
		fmt.Fprintf(b, "signingConfig = %s\n", tomlString(a.SigningConfig))
	}
	if len(a.BuildConfigFields) > 0 {
		fmt.Fprintf(b, "buildConfigFields = %s\n", tomlStringArray(a.BuildConfigFields))
	}
	if a.SelectedFlavor != "" {
		fmt.Fprintf(b, "selectedFlavor = %s\n", tomlString(a.SelectedFlavor))
	}
	if a.FlavorVersionNameSuffix != "" {
		fmt.Fprintf(b, "flavorVersionNameSuffix = %s\n", tomlString(a.FlavorVersionNameSuffix))
	}
	if a.SplitABIEnable {
		fmt.Fprintf(b, "splitAbiEnable = %s\n", tomlBool(a.SplitABIEnable))
	}
	if a.SplitABIUniversalDecl != nil {
		fmt.Fprintf(b, "splitAbiUniversalApk = %s\n", tomlBool(*a.SplitABIUniversalDecl))
	}
	if len(a.SplitABIInclude) > 0 {
		fmt.Fprintf(b, "splitAbiInclude = %s\n", tomlStringArray(a.SplitABIInclude))
	}

	for _, name := range sortedMapKeys(a.SigningConfigs) {
		s := a.SigningConfigs[name]
		fmt.Fprintf(b, "\n[modules.android.signingConfigs.%s]\n", tomlKey(name))
		if s.StoreFile != "" {
			fmt.Fprintf(b, "storeFile = %s\n", tomlString(s.StoreFile))
		}
		if s.StorePassword != "" {
			fmt.Fprintf(b, "storePassword = %s\n", tomlString(s.StorePassword))
		}
		if s.KeyAlias != "" {
			fmt.Fprintf(b, "keyAlias = %s\n", tomlString(s.KeyAlias))
		}
		if s.KeyPassword != "" {
			fmt.Fprintf(b, "keyPassword = %s\n", tomlString(s.KeyPassword))
		}
	}
	for _, name := range sortedMapKeys(a.Flavors) {
		f := a.Flavors[name]
		fmt.Fprintf(b, "\n[modules.android.flavors.%s]\n", tomlKey(name))
		if f.VersionNameSuffix != "" {
			fmt.Fprintf(b, "versionNameSuffix = %s\n", tomlString(f.VersionNameSuffix))
		}
		if len(f.BuildConfigFields) > 0 {
			fmt.Fprintf(b, "buildConfigFields = %s\n", tomlStringArray(f.BuildConfigFields))
		}
		if len(f.ProguardFiles) > 0 {
			fmt.Fprintf(b, "proguardFiles = %s\n", tomlStringArray(f.ProguardFiles))
		}
	}
	for _, vc := range a.VariantConfigs {
		b.WriteString("\n[[modules.android.variants]]\n")
		if vc.BuildType != "" {
			fmt.Fprintf(b, "buildType = %s\n", tomlString(vc.BuildType))
		}
		if len(vc.LocaleFilters) > 0 {
			fmt.Fprintf(b, "localeFilters = %s\n", tomlStringArray(vc.LocaleFilters))
		}
		if vc.UseLegacyPackaging != nil {
			fmt.Fprintf(b, "useLegacyPackaging = %s\n", tomlBool(*vc.UseLegacyPackaging))
		}
		if len(vc.ResourceExcludes) > 0 {
			fmt.Fprintf(b, "resourceExcludes = %s\n", tomlStringArray(vc.ResourceExcludes))
		}
	}
}

func writeDependency(b *strings.Builder, dep Dependency) {
	b.WriteString("\n[[modules.dependencies]]\n")
	if dep.Scope != "" {
		fmt.Fprintf(b, "scope = %s\n", tomlString(dep.Scope))
	}
	switch {
	case dep.IsProject:
		fmt.Fprintf(b, "project = true\n")
		fmt.Fprintf(b, "path = %s\n", tomlString(dep.ProjectPath))
	case dep.Group != "" || dep.Artifact != "":
		if dep.Group != "" {
			fmt.Fprintf(b, "group = %s\n", tomlString(dep.Group))
		}
		if dep.Artifact != "" {
			fmt.Fprintf(b, "artifact = %s\n", tomlString(dep.Artifact))
		}
		if dep.Version != "" {
			fmt.Fprintf(b, "version = %s\n", tomlString(dep.Version))
		}
	}
	if dep.IsPlatform {
		fmt.Fprintf(b, "platform = true\n")
	}
}

// modulePathRelative 返回模块相对项目根的路径（POSIX 分隔）。
// 若模块在项目根之外（如 composite build），仍返回可用的相对路径（含 ..）。
// 解析时该路径会基于项目根重新拼接，因此必须相对根而非相对 cwd。
func modulePathRelative(rootDir, modPath string) string {
	if modPath == "" {
		return ""
	}
	if !filepath.IsAbs(modPath) || rootDir == "" {
		return filepath.ToSlash(modPath)
	}
	rel, err := filepath.Rel(rootDir, modPath)
	if err != nil {
		return filepath.ToSlash(modPath)
	}
	return filepath.ToSlash(rel)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---- TOML 值格式化 ----

func tomlString(s string) string {
	return strconv.Quote(s)
}

func tomlBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func tomlFloat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func tomlStringArray(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = tomlString(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// tomlKey 对含特殊字符的 key 加引号。
func tomlKey(k string) string {
	if k == "" {
		return `""`
	}
	for _, r := range k {
		if !(r == '_' || r == '-' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return strconv.Quote(k)
		}
	}
	return k
}
