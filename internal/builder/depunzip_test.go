package builder

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// buildAARWithLibs 构造含 classes.jar 与 libs/extra.jar 的 AAR
func buildAARWithLibs(t *testing.T) []byte {
	t.Helper()
	mkJar := func(className string) []byte {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		f, _ := zw.Create(className)
		f.Write([]byte{0xCA, 0xFE, 0xBA, 0xBE})
		zw.Close()
		return b.Bytes()
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	cf, _ := zw.Create("classes.jar")
	cf.Write(mkJar("com/example/Main.class"))
	lf, _ := zw.Create("libs/extra.jar")
	lf.Write(mkJar("androidx/emoji2/text/flatbuffer/MetadataList.class"))
	zw.Close()
	return out.Bytes()
}

// TestUnzipDepExtractsAARLibsJars AAR 内嵌 libs/*.jar 必须被解压并纳入 aux 列表
func TestUnzipDepExtractsAARLibsJars(t *testing.T) {
	depsDir := t.TempDir()
	aarPath := filepath.Join(t.TempDir(), "foo-1.0.aar")
	if err := os.WriteFile(aarPath, buildAARWithLibs(t), 0644); err != nil {
		t.Fatal(err)
	}

	unzipDepTo("com.example", "foo", aarPath, depsDir)

	depDir := filepath.Join(depsDir, depDirName("com.example", "foo"))
	if !fileExists(filepath.Join(depDir, "classes.jar")) {
		t.Fatalf("classes.jar 未解压: %s", depDir)
	}
	auxJar := filepath.Join(depDir, "libs", "extra.jar")
	if !fileExists(auxJar) {
		t.Fatalf("AAR libs/extra.jar 未解压: %s", auxJar)
	}
	jars := depAuxJars(depDir)
	if len(jars) != 1 || jars[0] != auxJar {
		t.Fatalf("depAuxJars 应返回 [%s]，实际: %v", auxJar, jars)
	}
}
