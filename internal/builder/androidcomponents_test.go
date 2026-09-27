package builder

import (
	"os"
	"path/filepath"
	"testing"

	"canter/internal/engine"
	"canter/internal/parser"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"META-INF/*.version", "META-INF/androidx.core_core.version", true},
		{"META-INF/*.version", "META-INF/a/b.version", false}, // * 不跨目录
		{"**/*.kotlin_builtins", "kotlin/ranges/ranges.kotlin_builtins", true},
		{"**/*.kotlin_builtins", "kotlin/kotlin.kotlin_builtins", true},
		{"META-INF/services/x.Y", "META-INF/services/x.Y", true},
		{"META-INF/*.version", "META-INF/MANIFEST.MF", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.name); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestIsSignatureLike(t *testing.T) {
	kept := []string{
		"META-INF/androidx.core_core.version",
		"META-INF/services/kotlinx.coroutines.MainDispatcherFactory",
		"kotlin/kotlin.kotlin_builtins",
		"META-INF/androidx/lifecycle/lifecycle-common/LICENSE.txt",
	}
	for _, n := range kept {
		if isSignatureLike(n) {
			t.Errorf("%s 不应被排除", n)
		}
	}
	dropped := []string{
		"META-INF/MANIFEST.MF",
		"META-INF/CERT.RSA", "META-INF/CERT.SF", "module-info.class",
		"META-INF/activity.kotlin_module",
		"META-INF/versions/9/OSGI-INF/MANIFEST.MF",
		"META-INF/proguard/coroutines.pro",
		"META-INF/maven/com.google.guava/x/pom.xml",
	}
	for _, n := range dropped {
		if !isSignatureLike(n) {
			t.Errorf("%s 应被排除", n)
		}
	}
}

func TestMergeDataJarCollectsNonClassEntries(t *testing.T) {
	buildDir := t.TempDir()
	ctx := buildCtxForMerge(buildDir)
	depDir := filepath.Join(buildDir, "deps", "g_a")
	os.MkdirAll(depDir, 0755)
	makeClassesJar(t, filepath.Join(depDir, "classes.jar"), map[string]string{
		"com/a/A.class":                       "class-bytes",
		"META-INF/androidx.core_core.version": "1.19.0",
		"META-INF/MANIFEST.MF":                "manifest",
	})
	dataJar := filepath.Join(buildDir, "merged_data.jar")
	if err := mergeDataJar(ctx, dataJar); err != nil {
		t.Fatal(err)
	}
	entries := dataJarEntries(dataJar, nil)
	if _, ok := entries["com/a/A.class"]; ok {
		t.Errorf(".class 条目不应进入数据资源")
	}
	if v, ok := entries["META-INF/androidx.core_core.version"]; !ok || string(v) != "1.19.0" {
		t.Errorf("version 文件应保留，实际: %v", entries)
	}
	if _, ok := entries["META-INF/MANIFEST.MF"]; ok {
		t.Errorf("MANIFEST.MF 应被排除")
	}
	// 应用 excludes 后
	entries = dataJarEntries(dataJar, []string{"META-INF/*.version"})
	if _, ok := entries["META-INF/androidx.core_core.version"]; ok {
		t.Errorf("excludes 应过滤 version 文件")
	}
}

func buildCtxForMerge(buildDir string) *engine.BuildContext {
	return &engine.BuildContext{BuildDir: buildDir}
}

// TestParserAndroidComponents 解析 onVariants 定制
func TestParserAndroidComponents(t *testing.T) {
	src := `
androidComponents {
    onVariants(selector().withBuildType("release")) { variant ->
        variant.androidResources.localeFilters.addAll("en", "zh")
        variant.packaging.jniLibs.useLegacyPackaging.set(true)
        variant.packaging.jniLibs.useLegacyPackagingFromBundle.set(true)
        variant.packaging.resources.excludes.addAll("META-INF/*.version", "**/*.kotlin_builtins")
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "build.gradle.kts")
	os.WriteFile(path, []byte(src), 0644)
	mod := parser.BuildFileParser{}.Parse(path, parser.NewVersionCatalog(), map[string]string{})
	if mod.Android == nil || len(mod.Android.VariantConfigs) != 1 {
		t.Fatalf("应解析出 1 个 VariantConfig，实际 %+v", mod.Android)
	}
	vc := mod.Android.VariantConfigs[0]
	if vc.BuildType != "release" {
		t.Errorf("BuildType 应为 release，实际 %q", vc.BuildType)
	}
	if len(vc.LocaleFilters) != 2 || vc.LocaleFilters[0] != "en" || vc.LocaleFilters[1] != "zh" {
		t.Errorf("localeFilters 解析异常: %v", vc.LocaleFilters)
	}
	if vc.UseLegacyPackaging == nil || !*vc.UseLegacyPackaging {
		t.Errorf("useLegacyPackaging 应为 true")
	}
	if len(vc.ResourceExcludes) != 2 {
		t.Errorf("excludes 解析异常: %v", vc.ResourceExcludes)
	}
}
