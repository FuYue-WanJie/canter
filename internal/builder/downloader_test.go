package builder

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeAAR 构造内存中的 AAR：withClass=true 时 classes.jar 内含一个 .class，
// 否则 classes.jar 仅含版本元数据（空壳 AAR，如 androidx core-ktx 1.19.0）。
func makeAAR(withClass bool) []byte {
	var inner bytes.Buffer
	iw := zip.NewWriter(&inner)
	if withClass {
		f, _ := iw.Create("com/example/Foo.class")
		f.Write([]byte{0xCA, 0xFE, 0xBA, 0xBE})
	} else {
		f, _ := iw.Create("META-INF/foo.version")
		f.Write([]byte("1.0"))
	}
	iw.Close()

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	cf, _ := zw.Create("classes.jar")
	cf.Write(inner.Bytes())
	zw.Close()
	return out.Bytes()
}

const testPOM = `<?xml version="1.0" encoding="UTF-8"?>
<project><modelVersion>4.0.0</modelVersion><packaging>aar</packaging></project>`

// newArtifactServer 提供 Maven 风格的 POM/AAR 响应
func newArtifactServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := files[r.URL.Path]; ok {
			w.WriteHeader(http.StatusOK)
			w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDownloadPrefersRealVariantOverStub 保证 KMP 场景下含类的 -android 变体优先于空壳基础构件
func TestDownloadPrefersRealVariantOverStub(t *testing.T) {
	files := map[string][]byte{
		"/com/example/foo/1.0/foo-1.0.pom":                 []byte(testPOM),
		"/com/example/foo/1.0/foo-1.0.aar":                 makeAAR(false),
		"/com/example/foo-android/1.0/foo-android-1.0.pom": []byte(testPOM),
		"/com/example/foo-android/1.0/foo-android-1.0.aar": makeAAR(true),
	}
	srv := newArtifactServer(t, files)

	d := NewDownloader([]string{srv.URL}, t.TempDir())
	d.NoGradleCache = true

	got, err := d.Download("com.example", "foo", "1.0")
	if err != nil {
		t.Fatalf("Download 返回错误: %v", err)
	}
	if !strings.Contains(got, "foo-android-1.0.aar") {
		t.Fatalf("应优先选择含类的 -android 变体，实际得到: %s", got)
	}
	if !strings.HasSuffix(got, ".aar") {
		t.Fatalf("期望 .aar，实际得到: %s", got)
	}
}

// TestDownloadAcceptsEmptyAARWhenNoVariant 空壳 AAR 在无更好变体时应被接受而非报失败
func TestDownloadAcceptsEmptyAARWhenNoVariant(t *testing.T) {
	files := map[string][]byte{
		"/com/example/foo/1.0/foo-1.0.pom": []byte(testPOM),
		"/com/example/foo/1.0/foo-1.0.aar": makeAAR(false),
	}
	srv := newArtifactServer(t, files)

	d := NewDownloader([]string{srv.URL}, t.TempDir())
	d.NoGradleCache = true

	got, err := d.Download("com.example", "foo", "1.0")
	if err != nil {
		t.Fatalf("空壳 AAR 不应报失败，返回错误: %v", err)
	}
	if strings.Contains(got, "-android") {
		t.Fatalf("不应凭空选择不存在的变体: %s", got)
	}
	if !fileExists(got) {
		t.Fatalf("返回的路径不存在: %s", got)
	}
}

// TestDownloadCachedStubStillFallsBackToVariant 已缓存空壳基础构件时，仍应选用网络上含类的 -android 变体
func TestDownloadCachedStubStillFallsBackToVariant(t *testing.T) {
	files := map[string][]byte{
		"/com/example/foo-android/1.0/foo-android-1.0.pom": []byte(testPOM),
		"/com/example/foo-android/1.0/foo-android-1.0.aar": makeAAR(true),
	}
	srv := newArtifactServer(t, files)

	cache := t.TempDir()
	// 预置已缓存的基础空壳 AAR
	cachedStub := filepath.Join(cache, "com", "example", "foo", "1.0", "foo-1.0.aar")
	if err := os.MkdirAll(filepath.Dir(cachedStub), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedStub, makeAAR(false), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDownloader([]string{srv.URL}, cache)
	d.NoGradleCache = true

	got, err := d.Download("com.example", "foo", "1.0")
	if err != nil {
		t.Fatalf("Download 返回错误: %v", err)
	}
	if !strings.Contains(got, "foo-android-1.0.aar") {
		t.Fatalf("缓存空壳不应短路变体回退，实际得到: %s", got)
	}
}
