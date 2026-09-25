package builder

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"canter/internal/engine"
)

func writeClassFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{0xCA, 0xFE, 0xBA, 0xBE}, 0644); err != nil {
		t.Fatal(err)
	}
}

func makeClassesJar(t *testing.T, path string, classes map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range classes {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	zw.Close()
	f.Close()
}

func readJarEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 64)
		n, _ := rc.Read(buf)
		rc.Close()
		out[f.Name] = string(buf[:n])
	}
	return out
}

// TestMergeClassesJarFirstWinsAndFilters 工程类优先于依赖类，且只纳入 .class
func TestMergeClassesJarFirstWinsAndFilters(t *testing.T) {
	buildDir := t.TempDir()
	ctx := &engine.BuildContext{BuildDir: buildDir}

	writeClassFile(t, filepath.Join(buildDir, "classes", "com", "a", "A.class"))
	writeClassFile(t, filepath.Join(buildDir, "kotlin_classes", "com", "a", "B.class"))
	// 依赖 jar：A 与工程类重名（应保留工程类内容），另含 C 与非 .class 条目
	makeClassesJar(t, filepath.Join(buildDir, "deps", "g_a", "classes.jar"), map[string]string{
		"com/a/A.class":        "from-dep",
		"com/a/C.class":        "dep-c",
		"META-INF/MANIFEST.MF": "ignore-me",
	})

	jarFile := filepath.Join(buildDir, "merged_classes.jar")
	if err := mergeClassesJar(ctx, jarFile); err != nil {
		t.Fatalf("mergeClassesJar 失败: %v", err)
	}

	entries := readJarEntries(t, jarFile)
	for _, name := range []string{"com/a/A.class", "com/a/B.class", "com/a/C.class"} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("缺少条目 %s，实际: %v", name, keysOf(entries))
		}
	}
	if _, ok := entries["META-INF/MANIFEST.MF"]; ok {
		t.Fatalf("非 .class 条目不应被纳入")
	}
	if entries["com/a/A.class"] == "from-dep" {
		t.Fatalf("重名类应保留工程类内容（先到先得）")
	}

	// jar 内不得出现重复条目名
	zr, err := zip.OpenReader(jarFile)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	seen := map[string]int{}
	for _, f := range zr.File {
		seen[f.Name]++
	}
	if seen["com/a/A.class"] != 1 {
		t.Fatalf("重名类应只出现一次，实际 %d 次", seen["com/a/A.class"])
	}
}

func keysOf(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
