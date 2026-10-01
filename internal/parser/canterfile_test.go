package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanterConfigRoundTrip 验证 ProjectConfig -> canter.toml -> ProjectConfig 无损（关键字段）。
func TestCanterConfigRoundTrip(t *testing.T) {
	conv := func(v float64) *float64 { return &v }
	ci := func(v int) *int { return &v }
	tb := func(v bool) *bool { return &v }

	config := &ProjectConfig{
		RootDir:     "/proj",
		ProjectName: "Demo",
		Repositories: []string{
			"https://dl.google.com/dl/android/maven2",
			"https://repo.maven.apache.org/maven2",
		},
		PluginRepositories: []string{"https://plugins.gradle.org/maven2"},
		Catalog:            NewVersionCatalog(),
		GradleProperties:   map[string]string{"org.gradle.jvmargs": "-Xmx4g"},
	}
	config.Catalog.Versions["kotlin"] = "2.1.0"
	config.Catalog.Libraries["androidx.core.ktx"] = map[string]string{"group": "androidx.core", "name": "core-ktx", "version.ref": "kotlin"}
	config.Catalog.Plugins["android-application"] = map[string]string{"id": "com.android.application", "version.ref": "kotlin"}
	config.Catalog.Bundles["ui"] = []string{"a", "b"}

	config.Modules = []ModuleConfig{{
		Name:    "app",
		Path:    filepath.Join("/proj", "app"),
		Plugins: []string{"com.android.application"},
		BuildFeatures: map[string]bool{
			"compose": true,
		},
		PackagingExcludes: []string{"META-INF/LICENSE"},
		Android: &AndroidConfig{
			Namespace:             "com.demo",
			CompileSDK:            conv(37.1),
			ApplicationID:         "com.demo",
			MinSDK:                ci(30),
			TargetSDK:             ci(37),
			VersionCode:           ci(5),
			VersionName:           "1.2.3",
			JvmTarget:             "17",
			MinifyEnabled:         true,
			ShrinkResources:       true,
			ProguardFiles:         []string{"proguard-rules.pro"},
			ABIFilters:            []string{"arm64-v8a"},
			SourceCompatibility:   "17",
			TargetCompatibility:   "17",
			SigningConfig:         "release",
			BuildConfigFields:     []string{"String:API:https://x"},
			SelectedFlavor:        "full",
			SplitABIEnable:        true,
			SplitABIUniversalDecl: tb(true),
			SplitABIInclude:       []string{"arm64-v8a"},
			SigningConfigs: map[string]SigningConfigEntry{
				"release": {StoreFile: "file:keystore.jks", StorePassword: "env:KS_PASS", KeyAlias: "k", KeyPassword: "env:K_PASS"},
			},
			Flavors: map[string]FlavorConfig{
				"full": {VersionNameSuffix: "-full", BuildConfigFields: []string{"boolean:FULL:true"}},
			},
			VariantConfigs: []VariantConfig{
				{BuildType: "release", LocaleFilters: []string{"en", "zh"}, UseLegacyPackaging: tb(true), ResourceExcludes: []string{"*.bak"}},
			},
		},
		Dependencies: []Dependency{
			{Scope: "implementation", Group: "androidx.core", Artifact: "core-ktx", Version: "1.15.0"},
			{Scope: "implementation", IsProject: true, ProjectPath: ":lib"},
			{Scope: "implementation", IsPlatform: true, Group: "androidx.compose", Artifact: "compose-bom", Version: "2024.01.00"},
		},
	}}

	text := WriteCanterConfig(config)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, CanterFileName), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}

	got, ok := ParseCanterConfig(dir)
	if !ok {
		t.Fatal("ParseCanterConfig 返回 false")
	}
	assertEq(t, "ProjectName", got.ProjectName, "Demo")
	if got.Catalog.Versions["kotlin"] != "2.1.0" {
		t.Errorf("versions.kotlin = %q", got.Catalog.Versions["kotlin"])
	}
	if len(got.Catalog.Libraries) != 1 || got.Catalog.Libraries["androidx.core.ktx"]["group"] != "androidx.core" {
		t.Errorf("libraries 往返失败: %+v", got.Catalog.Libraries)
	}
	if len(got.Modules) != 1 {
		t.Fatalf("模块数 = %d", len(got.Modules))
	}
	m := got.Modules[0]
	if m.Android == nil {
		t.Fatal("Android 配置丢失")
	}
	a := m.Android
	assertEq(t, "namespace", a.Namespace, "com.demo")
	if a.CompileSDK == nil || *a.CompileSDK != 37.1 {
		t.Errorf("compileSdk = %v", a.CompileSDK)
	}
	if a.VersionCode == nil || *a.VersionCode != 5 {
		t.Errorf("versionCode = %v", a.VersionCode)
	}
	assertEq(t, "versionName", a.VersionName, "1.2.3")
	if !a.MinifyEnabled || !a.ShrinkResources {
		t.Error("minify/shrink 丢失")
	}
	if len(a.ABIFilters) != 1 || a.ABIFilters[0] != "arm64-v8a" {
		t.Errorf("abiFilters = %v", a.ABIFilters)
	}
	if a.SplitABIUniversalDecl == nil || !*a.SplitABIUniversalDecl {
		t.Error("splitAbiUniversalApk 丢失")
	}
	if s, ok := a.SigningConfigs["release"]; !ok || s.StorePassword != "env:KS_PASS" {
		t.Errorf("signingConfigs 往返失败: %+v", a.SigningConfigs)
	}
	if f, ok := a.Flavors["full"]; !ok || f.VersionNameSuffix != "-full" {
		t.Errorf("flavors 往返失败: %+v", a.Flavors)
	}
	if len(a.VariantConfigs) != 1 || a.VariantConfigs[0].UseLegacyPackaging == nil || !*a.VariantConfigs[0].UseLegacyPackaging {
		t.Errorf("variantConfigs 往返失败: %+v", a.VariantConfigs)
	}
	if len(m.Dependencies) != 3 {
		t.Fatalf("依赖数 = %d", len(m.Dependencies))
	}
	if !m.Dependencies[1].IsProject || m.Dependencies[1].ProjectPath != ":lib" {
		t.Errorf("project 依赖往返失败: %+v", m.Dependencies[1])
	}
	if !m.Dependencies[2].IsPlatform {
		t.Errorf("platform 依赖往返失败: %+v", m.Dependencies[2])
	}
}

// TestGradleToCanterToGradle 以真实 WearPomodoro 工程跑 Gradle->Canter->Gradle 往返，
// 断言第二轮 Gradle 解析结果与第一轮一致（模块数、依赖数、Android 关键字段）。
func TestGradleToCanterToGradle(t *testing.T) {
	src := "/workspace/WearPomodoro"
	if _, err := os.Stat(src); err != nil {
		t.Skip("跳过：WearPomodoro 不存在")
	}
	first := GradleConfigParser{}.parseGradle(src)
	if len(first.Modules) == 0 {
		t.Fatal("Gradle 解析无模块")
	}

	// Gradle -> canter.toml
	dir := t.TempDir()
	canterText := WriteCanterConfig(first)
	if err := os.WriteFile(filepath.Join(dir, CanterFileName), []byte(canterText), 0644); err != nil {
		t.Fatal(err)
	}
	// canter.toml -> ProjectConfig
	second, ok := ParseCanterConfig(dir)
	if !ok {
		t.Fatal("canter 解析失败")
	}
	// canter.toml -> Gradle 文件 -> 再解析
	files := GenerateGradleFiles(second)
	if files["settings.gradle.kts"] == "" {
		t.Fatal("未生成 settings.gradle.kts")
	}
	// 用生成的 Gradle 文件重建一个临时工程目录
	dir2 := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir2, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	third := GradleConfigParser{}.parseGradle(dir2)

	// composite build 模块由 includeBuild 声明，第二轮解析依赖被包含构建自身；
	// 这里仅校验根模块（非 composite）的往返一致性。
	byName := func(mods []ModuleConfig) map[string]ModuleConfig {
		m := map[string]ModuleConfig{}
		for _, mm := range mods {
			if mm.IncludeBuild != "" {
				continue
			}
			m[mm.Name] = mm
		}
		return m
	}

	assertEqInt(t, "模块数", len(byName(third.Modules)), len(byName(first.Modules)))
	if len(third.Repositories) != len(first.Repositories) {
		t.Errorf("仓库数 %d vs %d", len(third.Repositories), len(first.Repositories))
	}
	fm, tm := byName(first.Modules), byName(third.Modules)
	for name, orig := range fm {
		round, ok := tm[name]
		if !ok {
			t.Errorf("模块 %s 在往返后丢失", name)
			continue
		}
		if len(round.Dependencies) != len(orig.Dependencies) {
			t.Errorf("模块 %s 依赖数 %d vs %d", name, len(round.Dependencies), len(orig.Dependencies))
		}
		if orig.Android == nil {
			continue
		}
		if round.Android == nil {
			t.Errorf("模块 %s Android 配置丢失", name)
			continue
		}
		assertEq(t, name+".namespace", round.Android.Namespace, orig.Android.Namespace)
		assertEq(t, name+".applicationId", round.Android.ApplicationID, orig.Android.ApplicationID)
		assertEq(t, name+".versionName", round.Android.VersionName, orig.Android.VersionName)
		assertPtrEq(t, name+".minSdk", round.Android.MinSDK, orig.Android.MinSDK)
		assertPtrEq(t, name+".targetSdk", round.Android.TargetSDK, orig.Android.TargetSDK)
		assertPtrEq(t, name+".versionCode", round.Android.VersionCode, orig.Android.VersionCode)
	}
}

// TestCanterConfigCoexistence 验证 canter.toml 存在时优先于 Gradle 配置。
func TestCanterConfigCoexistence(t *testing.T) {
	dir := t.TempDir()
	// 一个空的 Gradle settings（会被忽略）
	if err := os.WriteFile(filepath.Join(dir, "settings.gradle.kts"), []byte("rootProject.name = \"FromGradle\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// 写 canter.toml
	cc := "[project]\nname = \"FromCanter\"\n"
	if err := os.WriteFile(filepath.Join(dir, CanterFileName), []byte(cc), 0644); err != nil {
		t.Fatal(err)
	}
	config, source := LoadProject(dir)
	if source != "canter" {
		t.Errorf("来源 = %q，期望 canter", source)
	}
	if config.ProjectName != "FromCanter" {
		t.Errorf("项目名 = %q", config.ProjectName)
	}

	// 删除 canter.toml 后应回退 Gradle
	os.Remove(filepath.Join(dir, CanterFileName))
	config, source = LoadProject(dir)
	if source != "gradle" {
		t.Errorf("来源 = %q，期望 gradle", source)
	}
	if config.ProjectName != "FromGradle" {
		t.Errorf("回退项目名 = %q", config.ProjectName)
	}
}

// TestTomlParserBasics 覆盖 TOML 解析器的数组表、内联字典、跨行。
func TestTomlParserBasics(t *testing.T) {
	text := `
[project]
name = "X"
repositories = ["google", "mavenCentral"]

[libraries]
a-b = { group = "g", name = "n", version.ref = "v1" }

[[modules]]
name = "m1"
plugins = ["p1", "p2"]

[modules.android]
namespace = "ns"
compileSdk = 37.1

[[modules.dependencies]]
scope = "implementation"
group = "g"
artifact = "a"
`
	doc := parseTOML(text)
	cc := decodeCanterConfig(doc)
	if cc.Project.Name != "X" {
		t.Errorf("name = %q", cc.Project.Name)
	}
	if len(cc.Project.Repositories) != 2 || cc.Project.Repositories[0] != "google" {
		t.Errorf("repositories = %v", cc.Project.Repositories)
	}
	if cc.Libraries["a-b"].VersionRef != "v1" {
		t.Errorf("library = %+v", cc.Libraries["a-b"])
	}
	if len(cc.Modules) != 1 {
		t.Fatalf("modules = %d", len(cc.Modules))
	}
	if cc.Modules[0].Android == nil || cc.Modules[0].Android.Namespace != "ns" {
		t.Errorf("android = %+v", cc.Modules[0].Android)
	}
	if cc.Modules[0].Android.CompileSDK == nil || *cc.Modules[0].Android.CompileSDK != 37.1 {
		t.Errorf("compileSdk = %v", cc.Modules[0].Android.CompileSDK)
	}
	if len(cc.Modules[0].Dependencies) != 1 || cc.Modules[0].Dependencies[0].Artifact != "a" {
		t.Errorf("依赖 = %+v", cc.Modules[0].Dependencies)
	}
}

func assertEq(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q，期望 %q", label, got, want)
	}
}

func assertEqInt(t *testing.T, label string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d，期望 %d", label, got, want)
	}
}

func assertPtrEq(t *testing.T, label string, got, want *int) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Errorf("%s = %v，期望 %v", label, got, want)
		}
		return
	}
	if *got != *want {
		t.Errorf("%s = %d，期望 %d", label, *got, *want)
	}
}

var _ = strings.TrimSpace
