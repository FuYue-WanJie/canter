package builder

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 非 Android 平台后缀
var nonAndroidSuffixes = []string{
	"-desktop", "-jvmstubs", "-lint", "-linuxx64", "-macosx64",
	"-macosarm64", "-windows", "-wasm", "-js", "-metadata",
}

// 测试后缀
var testSuffixes = []string{"-testing", "-test", "-debug", "-androidTest"}

const JitpackRepo = "https://jitpack.io"

// Downloader 依赖下载器
type Downloader struct {
	Repos     []string
	CacheDir  string
	client    *http.Client
}

// NewDownloader 创建下载器
func NewDownloader(repos []string, cacheDir string) *Downloader {
	os.MkdirAll(cacheDir, 0755)
	return &Downloader{
		Repos:    repos,
		CacheDir: cacheDir,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func hasSuffix(s string, suffixes []string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// aarClassesJarHasClass 判断 AAR 内的 classes.jar 是否含实际 .class 条目
func aarClassesJarHasClass(aarPath string) bool {
	zr, err := zip.OpenReader(aarPath)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "classes.jar" {
			continue
		}
		if f.UncompressedSize64 == 0 {
			return false
		}
		rc, err := f.Open()
		if err != nil {
			return false
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return false
		}
		inner, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return false
		}
		for _, in := range inner.File {
			if strings.HasSuffix(in.Name, ".class") {
				return true
			}
		}
		return false
	}
	return false
}

// IsStubAAR 判断是否为 stub AAR（classes.jar 缺失或为空，无实际类实现）
func (d *Downloader) IsStubAAR(aarPath string) bool {
	if !strings.HasSuffix(aarPath, ".aar") {
		return false
	}
	// 若 AAR 的 classes.jar 内没有任何 .class，视为 stub
	if !aarClassesJarHasClass(aarPath) {
		return true
	}
	return false
}

// IsValidArtifact 判断 artifact 是否有效
func (d *Downloader) IsValidArtifact(artPath string) bool {
	zr, err := zip.OpenReader(artPath)
	if err != nil {
		return false
	}
	defer zr.Close()
	if strings.HasSuffix(artPath, ".aar") {
		return aarClassesJarHasClass(artPath)
	}
	if strings.HasSuffix(artPath, ".jar") {
		for _, f := range zr.File {
			if strings.HasSuffix(f.Name, ".class") {
				return true
			}
		}
		return false
	}
	return false
}

// FetchPOM 仅获取 POM（minibuild 缓存 → Gradle 缓存 → 短超时网络），用于依赖图遍历
func (d *Downloader) FetchPOM(group, artifact, version string) (string, error) {
	groupPath := strings.ReplaceAll(group, ".", "/")
	pomPath := filepath.Join(d.CacheDir, filepath.FromSlash(groupPath), artifact, version, artifact+"-"+version+".pom")
	if _, err := os.Stat(pomPath); err == nil {
		return pomPath, nil
	}
	// 优先复用 Gradle 已缓存的 POM（避免网络）
	if src := findGradleCachedPOM(group, artifact, version); src != "" {
		os.MkdirAll(filepath.Dir(pomPath), 0755)
		if copyFile(src, pomPath) == nil {
			return pomPath, nil
		}
		return src, nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, repo := range d.Repos {
		url := fmt.Sprintf("%s/%s/%s/%s/%s-%s.pom",
			strings.TrimRight(repo, "/"), groupPath, artifact, version, artifact, version)
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		os.MkdirAll(filepath.Dir(pomPath), 0755)
		out, cerr := os.Create(pomPath)
		if cerr == nil {
			io.Copy(out, resp.Body)
			out.Close()
		}
		resp.Body.Close()
		return pomPath, nil
	}
	return "", fmt.Errorf("pom not found: %s:%s:%s", group, artifact, version)
}

// findGradleCachedPOM 在 Gradle 缓存中查找已下载的 POM
func findGradleCachedPOM(group, artifact, version string) string {
	return findGradleCachedFile(group, artifact, version, artifact+"-"+version+".pom")
}

// findGradleCachedArtifact 在 Gradle 缓存中查找已下载的 aar/jar
func findGradleCachedArtifact(group, artifact, version, ext string) string {
	if p := findGradleCachedFile(group, artifact, version, artifact+"-"+version+"."+ext); p != "" {
		return p
	}
	// 回退：Gradle 对 KMP 变体可能用基础名存储文件，按扩展名扫描版本目录
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	groupPath := strings.ReplaceAll(group, ".", "/")
	versionDir := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1",
		filepath.FromSlash(groupPath), artifact, version)
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(versionDir, e.Name()))
		for _, f := range files {
			name := f.Name()
			if strings.HasSuffix(name, "."+ext) && !strings.Contains(name, "sources") && !strings.Contains(name, "javadoc") {
				return filepath.Join(versionDir, e.Name(), name)
			}
		}
	}
	return ""
}

// findGradleCachedFile 在 Gradle modules-2 缓存中按文件名查找
func findGradleCachedFile(group, artifact, version, target string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	groupPath := strings.ReplaceAll(group, ".", "/")
	versionDir := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1",
		filepath.FromSlash(groupPath), artifact, version)
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			p := filepath.Join(versionDir, e.Name(), target)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// Download 下载依赖
func (d *Downloader) Download(group, artifact, version string) (string, error) {
	// 后缀过滤
	if hasSuffix(artifact, nonAndroidSuffixes) || hasSuffix(artifact, testSuffixes) {
		return "", fmt.Errorf("非 Android 平台变体: %s", artifact)
	}

	groupPath := strings.ReplaceAll(group, ".", "/")

	// 缓存检查（minibuild 缓存 → Gradle 缓存）
	for _, artName := range []string{artifact, artifact + "-android", artifact + "-release", artifact + "-jvm"} {
		for _, ext := range []string{"aar", "jar"} {
			path := filepath.Join(d.CacheDir, filepath.FromSlash(groupPath), artName, version, artName+"-"+version+"."+ext)
			if _, err := os.Stat(path); err == nil && d.IsValidArtifact(path) {
				return path, nil
			}
			// 复用 Gradle 已缓存的 artifact（避免网络下载）
			if src := findGradleCachedArtifact(group, artName, version, ext); src != "" {
				if d.IsValidArtifact(src) {
					os.MkdirAll(filepath.Dir(path), 0755)
					if copyFile(src, path) == nil {
						return path, nil
					}
					return src, nil
				}
			}
		}
	}

	// GitHub 依赖走 JitPack
	if strings.HasPrefix(group, "com.github") {
		return d.downloadFromJitpack(group, artifact, version)
	}

	// 变体 + 仓库双重循环
	var bestResult string
	for _, artName := range []string{artifact, artifact + "-android", artifact + "-release", artifact + "-jvm"} {
		for _, repo := range d.Repos {
			result, err := d.downloadFromRepo(group, artName, version, repo)
			if err != nil {
				continue
			}
			if strings.HasSuffix(result, ".aar") || strings.HasSuffix(result, ".jar") {
				if d.IsValidArtifact(result) {
					return result, nil
				}
			}
			if bestResult == "" {
				bestResult = result
			}
		}
	}
	if bestResult != "" {
		return bestResult, nil
	}
	return "", fmt.Errorf("无法下载 %s:%s:%s", group, artifact, version)
}

func (d *Downloader) downloadFromJitpack(group, artifact, version string) (string, error) {
	groupPath := strings.ReplaceAll(group, ".", "/")
	for _, ext := range []string{"aar", "jar"} {
		url := fmt.Sprintf("%s/%s/%s/%s/%s-%s.%s", JitpackRepo, groupPath, artifact, version, artifact, version, ext)
		path := filepath.Join(d.CacheDir, filepath.FromSlash(groupPath), artifact, version, artifact+"-"+version+"."+ext)
		os.MkdirAll(filepath.Dir(path), 0755)
		if _, err := os.Stat(path); err != nil {
			if err := d.downloadTo(url, path); err != nil {
				continue
			}
		}
		return path, nil
	}
	return "", fmt.Errorf("jitpack 下载失败: %s:%s:%s", group, artifact, version)
}

type pomModel struct {
	XMLName    xml.Name `xml:"project"`
	Packaging  string   `xml:"packaging"`
	GroupID    string   `xml:"groupId"`
	ArtifactID string   `xml:"artifactId"`
	Version    string   `xml:"version"`
}

func (d *Downloader) downloadFromRepo(group, artifact, version, repo string) (string, error) {
	groupPath := strings.ReplaceAll(group, ".", "/")
	base := strings.TrimRight(repo, "/")

	pomURL := fmt.Sprintf("%s/%s/%s/%s/%s-%s.pom", base, groupPath, artifact, version, artifact, version)
	pomPath := filepath.Join(d.CacheDir, filepath.FromSlash(groupPath), artifact, version, artifact+"-"+version+".pom")
	os.MkdirAll(filepath.Dir(pomPath), 0755)
	if _, err := os.Stat(pomPath); err != nil {
		if err := d.downloadTo(pomURL, pomPath); err != nil {
			return "", err
		}
	}

	packaging := "jar"
	if data, err := os.ReadFile(pomPath); err == nil {
		var model pomModel
		if err := xml.Unmarshal(data, &model); err == nil && model.Packaging != "" {
			packaging = model.Packaging
		}
	}

	if packaging == "pom" {
		// 尝试下载同坐标 jar（区分聚合 POM 与真 BOM）
		jarURL := fmt.Sprintf("%s/%s/%s/%s/%s-%s.jar", base, groupPath, artifact, version, artifact, version)
		jarPath := filepath.Join(d.CacheDir, filepath.FromSlash(groupPath), artifact, version, artifact+"-"+version+".jar")
		if _, err := os.Stat(jarPath); err == nil {
			return jarPath, nil
		}
		if err := d.downloadTo(jarURL, jarPath); err == nil {
			return jarPath, nil
		}
		return pomPath, nil
	}

	ext := "jar"
	if packaging == "aar" {
		ext = "aar"
	}
	artURL := fmt.Sprintf("%s/%s/%s/%s/%s-%s.%s", base, groupPath, artifact, version, artifact, version, ext)
	artPath := filepath.Join(d.CacheDir, filepath.FromSlash(groupPath), artifact, version, artifact+"-"+version+"."+ext)
	if _, err := os.Stat(artPath); err != nil {
		if err := d.downloadTo(artURL, artPath); err != nil {
			return "", err
		}
	}
	if d.IsStubAAR(artPath) {
		return "", fmt.Errorf("stub AAR: %s", artPath)
	}
	return artPath, nil
}

func (d *Downloader) downloadTo(url, path string) error {
	resp, err := d.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	out.Close()
	return os.Rename(tmp, path)
}
