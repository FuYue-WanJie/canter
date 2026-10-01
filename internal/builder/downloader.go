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
	Repos         []string
	CacheDir      string
	NoGradleCache bool
	client        *http.Client
}

// NewDownloader 创建下载器
func NewDownloader(repos []string, cacheDir string) *Downloader {
	os.MkdirAll(cacheDir, 0755)
	return &Downloader{
		Repos:         repos,
		CacheDir:      cacheDir,
		NoGradleCache: os.Getenv("CANTER_NO_GRADLE_CACHE") != "",
		client:        &http.Client{Timeout: 60 * time.Second},
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

// isZipFile 判断文件是否为可解析的 zip（用于接受 classes.jar 为空的真实 AAR/JAR）
func isZipFile(p string) bool {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return false
	}
	zr.Close()
	return true
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

// FetchPOM 仅获取 POM（Canter 缓存 → Gradle 缓存 → 短超时网络），用于依赖图遍历
func (d *Downloader) FetchPOM(group, artifact, version string) (string, error) {
	groupPath := strings.ReplaceAll(group, ".", "/")
	pomPath := filepath.Join(d.CacheDir, filepath.FromSlash(groupPath), artifact, version, artifact+"-"+version+".pom")
	if _, err := os.Stat(pomPath); err == nil {
		return pomPath, nil
	}
	// 优先复用 Gradle 已缓存的 POM（避免网络）
	if !d.NoGradleCache {
		if src := findGradleCachedPOM(group, artifact, version); src != "" {
			os.MkdirAll(filepath.Dir(pomPath), 0755)
			if copyFile(src, pomPath) == nil {
				return pomPath, nil
			}
			return src, nil
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	var lastErr error
	for _, repo := range d.Repos {
		url := fmt.Sprintf("%s/%s/%s/%s/%s-%s.pom",
			strings.TrimRight(repo, "/"), groupPath, artifact, version, artifact, version)
		resp, err := httpGetWithRetry(client, url, 3)
		if err != nil {
			lastErr = err
			continue
		}
		if resp == nil {
			// 404：该仓库确实不存在，换下一个仓库
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
	if lastErr != nil {
		return "", fmt.Errorf("pom 获取网络失败: %s:%s:%s: %w", group, artifact, version, lastErr)
	}
	return "", fmt.Errorf("pom not found: %s:%s:%s", group, artifact, version)
}

// httpGetWithRetry 发起 GET，仅对瞬时错误（网络错误/5xx/429）重试。
// 返回 (resp, nil) 表示 200；返回 (nil, nil) 表示 404/410（确实不存在，不重试）；
// 返回 (nil, err) 表示重试耗尽后仍失败（网络问题）。
func httpGetWithRetry(client *http.Client, url string, maxAttempts int) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
		} else {
			switch {
			case resp.StatusCode == http.StatusOK:
				return resp, nil
			case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
				resp.Body.Close()
				return nil, nil
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				resp.Body.Close()
				lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			default:
				resp.Body.Close()
				return nil, nil
			}
		}
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}
	return nil, lastErr
}

// FetchModule 获取 Gradle Module Metadata（.module）：
// Canter 缓存 → Gradle 缓存（可禁用）→ 网络。找不到时返回错误（.module 为可选）。
func (d *Downloader) FetchModule(group, artifact, version string) (string, error) {
	groupPath := strings.ReplaceAll(group, ".", "/")
	name := artifact + "-" + version + ".module"
	modPath := filepath.Join(d.CacheDir, filepath.FromSlash(groupPath), artifact, version, name)
	if fi, err := os.Stat(modPath); err == nil && !fi.IsDir() {
		return modPath, nil
	}
	if !d.NoGradleCache {
		if src := findGradleModuleFile(group, artifact, version); src != "" {
			os.MkdirAll(filepath.Dir(modPath), 0755)
			if copyFile(src, modPath) == nil {
				return modPath, nil
			}
			return src, nil
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for _, repo := range d.Repos {
		url := fmt.Sprintf("%s/%s/%s/%s/%s", strings.TrimRight(repo, "/"), groupPath, artifact, version, name)
		resp, err := httpGetWithRetry(client, url, 3)
		if err != nil {
			continue
		}
		if resp == nil {
			continue
		}
		os.MkdirAll(filepath.Dir(modPath), 0755)
		out, cerr := os.Create(modPath)
		if cerr != nil {
			resp.Body.Close()
			continue
		}
		io.Copy(out, resp.Body)
		out.Close()
		resp.Body.Close()
		return modPath, nil
	}
	return "", fmt.Errorf("module metadata not found: %s:%s:%s", group, artifact, version)
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
	versionDir := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1",
		group, artifact, version)
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

// findGradleCachedFile 在 Gradle modules-2 缓存中按文件名查找（注意：Gradle 缓存的 group 目录用点号）
func findGradleCachedFile(group, artifact, version, target string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	versionDir := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1",
		group, artifact, version)
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

	// 缓存检查（Canter 缓存 → Gradle 缓存）。
	// stubFallback 记录"已成功获取但 classes.jar 为空"的构件：
	// 这类构件（如 androidx core-ktx 1.19.0 已成为空壳，真实类在 core）在无更好变体时应当接受，
	// 而不是报失败。优先返回含类的变体（如 KMP 的 -android）。
	var stubFallback string
	for _, artName := range []string{artifact, artifact + "-android", artifact + "-release", artifact + "-jvm"} {
		for _, ext := range []string{"aar", "jar"} {
			path := filepath.Join(d.CacheDir, filepath.FromSlash(groupPath), artName, version, artName+"-"+version+"."+ext)
			if _, err := os.Stat(path); err == nil {
				if d.IsValidArtifact(path) {
					return path, nil
				}
				if stubFallback == "" && isZipFile(path) {
					stubFallback = path
				}
			}
			// 复用 Gradle 已缓存的 artifact（避免网络下载）
			if !d.NoGradleCache {
				if src := findGradleCachedArtifact(group, artName, version, ext); src != "" {
					if d.IsValidArtifact(src) {
						os.MkdirAll(filepath.Dir(path), 0755)
						if copyFile(src, path) == nil {
							return path, nil
						}
						return src, nil
					}
					if stubFallback == "" && isZipFile(src) {
						stubFallback = src
					}
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
	if stubFallback != "" {
		return stubFallback, nil
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
