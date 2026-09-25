package builder

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"canter/internal/engine"
)

// Toolchain 解析构建工具链 jar（Kotlin 编译器及其编译器插件、stdlib、编译器运行时依赖）。
//
// 解析顺序：自有工具链目录（~/.canter/toolchain）→ Gradle 缓存（可禁用）→ 镜像下载。
// 所有 Kotlin 构件严格使用项目声明的同一版本，避免编译器与插件版本错配。
type Toolchain struct {
	Repos     []string
	Dir       string
	KotlinVer string
	NoGradle  bool

	client *http.Client

	mu    sync.Mutex
	memo  map[string]string // "group:artifact:version:ext" -> 本地路径（"" 表示解析失败）
	kmemo map[string]string // "jarName" -> 路径（工具链构件）
}

const (
	groupKotlin    = "org.jetbrains.kotlin"
	artifactKotlin = "kotlin-compiler-embeddable"
)

var (
	tcMu    sync.Mutex
	tcByCtx = map[*engine.BuildContext]*Toolchain{}
)

// ToolchainFor 返回该 BuildContext 对应的 Toolchain（单次构建内复用，含解析记忆）
func ToolchainFor(ctx *engine.BuildContext) *Toolchain {
	tcMu.Lock()
	defer tcMu.Unlock()
	if tc, ok := tcByCtx[ctx]; ok {
		return tc
	}
	tc := &Toolchain{
		Repos:     ctx.Repositories,
		Dir:       ctx.ToolchainDir,
		KotlinVer: ctx.KotlinVersion,
		NoGradle:  ctx.NoGradleCache,
		client:    &http.Client{Timeout: 180 * time.Second},
		memo:      map[string]string{},
		kmemo:     map[string]string{},
	}
	tcByCtx[ctx] = tc
	return tc
}

// version 返回工具链使用的 Kotlin 版本。项目未声明时回退到工具链目录/Gradle 缓存中
// 语义版本最高的 kotlin-compiler-embeddable。
func (tc *Toolchain) version() string {
	if tc.KotlinVer != "" {
		return tc.KotlinVer
	}
	if v := highestVersionInDir(filepath.Join(tc.Dir, filepath.FromSlash(groupPathOf(groupKotlin)), artifactKotlin)); v != "" {
		return v
	}
	if !tc.NoGradle {
		home, _ := os.UserHomeDir()
		if v := highestVersionInDir(filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1", groupKotlin, artifactKotlin)); v != "" {
			return v
		}
	}
	return ""
}

// highestVersionInDir 读取目录下形如版本号的子目录，返回语义最高者
func highestVersionInDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	return selectHighestVersion(versions)
}

// KotlinCompilerJar 返回 kotlin-compiler-embeddable jar
func (tc *Toolchain) KotlinCompilerJar() string {
	return tc.kotlinArtifact(artifactKotlin)
}

// KotlinStdlibJar 返回项目 Kotlin 版本对应的 kotlin-stdlib jar
func (tc *Toolchain) KotlinStdlibJar() string {
	return tc.kotlinArtifact("kotlin-stdlib")
}

// ComposeCompilerJar 返回 Compose 编译器插件（embeddable）
func (tc *Toolchain) ComposeCompilerJar() string {
	return tc.kotlinArtifact("kotlin-compose-compiler-plugin-embeddable")
}

// SerializationCompilerJar 返回 kotlin-serialization 编译器插件（embeddable）
func (tc *Toolchain) SerializationCompilerJar() string {
	return tc.kotlinArtifact("kotlin-serialization-compiler-plugin-embeddable")
}

// ParcelizeCompilerJar 返回 kotlin-parcelize 编译器插件
func (tc *Toolchain) ParcelizeCompilerJar() string {
	return tc.kotlinArtifact("kotlin-parcelize-compiler")
}

// ParcelizeRuntimeJar 返回 kotlin-parcelize 运行时注解库
func (tc *Toolchain) ParcelizeRuntimeJar() string {
	return tc.kotlinArtifact("kotlin-parcelize-runtime")
}

// kotlinArtifact 解析 org.jetbrains.kotlin 下指定 artifact 的 jar（项目 Kotlin 版本）
func (tc *Toolchain) kotlinArtifact(artifact string) string {
	v := tc.version()
	if v == "" {
		return ""
	}
	return tc.resolveJar(groupKotlin, artifact, v)
}

// CompilerClasspath 返回运行 K2JVMCompiler 所需的完整 classpath（编译器 + POM 声明的运行时依赖）。
// compilerJar 为已解析的编译器 jar；为空时内部重新解析。
func (tc *Toolchain) CompilerClasspath(compilerJar string) string {
	if compilerJar == "" {
		compilerJar = tc.KotlinCompilerJar()
	}
	if compilerJar == "" {
		return ""
	}
	ver := tc.version()
	if ver == "" {
		return compilerJar
	}

	seenGA := map[string]bool{}
	var jarList []string
	nameSeen := map[string]bool{}
	addJar := func(p string) {
		if p == "" {
			return
		}
		if nameSeen[p] {
			return
		}
		nameSeen[p] = true
		jarList = append(jarList, p)
	}
	addJar(compilerJar)

	type coord struct{ g, a, v string }
	queue := []coord{{groupKotlin, artifactKotlin, ver}}
	seenGA[groupKotlin+":"+artifactKotlin] = true

	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		pom := tc.resolvePOM(c.g, c.a, c.v)
		if pom == "" {
			continue
		}
		data, err := os.ReadFile(pom)
		if err != nil {
			continue
		}
		var model tcPOM
		if err := xml.Unmarshal(data, &model); err != nil {
			continue
		}
		for _, dep := range model.Dependencies {
			if strings.EqualFold(strings.TrimSpace(dep.Optional), "true") {
				continue
			}
			scope := strings.TrimSpace(dep.Scope)
			if scope != "" && scope != "compile" && scope != "runtime" {
				continue
			}
			if dep.GroupID == "" || dep.ArtifactID == "" || dep.Version == "" {
				continue
			}
			addJar(tc.resolveJar(dep.GroupID, dep.ArtifactID, dep.Version))
			if hasWildcardExclusion(dep) {
				continue
			}
			key := dep.GroupID + ":" + dep.ArtifactID
			if seenGA[key] {
				continue
			}
			seenGA[key] = true
			queue = append(queue, coord{dep.GroupID, dep.ArtifactID, dep.Version})
		}
	}
	return strings.Join(jarList, string(os.PathListSeparator))
}

// resolveJar 解析单个 jar：自有目录 → Gradle 缓存（可禁用）→ 镜像下载
func (tc *Toolchain) resolveJar(group, artifact, version string) string {
	key := group + ":" + artifact + ":" + version + ":jar"
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if p, ok := tc.memo[key]; ok {
		return p
	}
	tc.memo[key] = "" // 占位，失败时保持为空
	p := tc.resolve(group, artifact, version, "jar", func(local string) bool { return fileExists(local) })
	tc.memo[key] = p
	return p
}

// resolvePOM 解析单个 POM：自有目录 → Gradle 缓存（可禁用）→ 镜像下载
func (tc *Toolchain) resolvePOM(group, artifact, version string) string {
	key := group + ":" + artifact + ":" + version + ":pom"
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if p, ok := tc.memo[key]; ok {
		return p
	}
	tc.memo[key] = ""
	p := tc.resolve(group, artifact, version, "pom", func(local string) bool { return fileExists(local) })
	tc.memo[key] = p
	return p
}

// groupPathOf 把 Maven group 转为路径形式（点号 → 斜杠）
func groupPathOf(group string) string {
	return strings.ReplaceAll(group, ".", "/")
}

// resolve 统一的构件解析流程。ok 用于判断已有本地文件是否可用。
func (tc *Toolchain) resolve(group, artifact, version, ext string, ok func(string) bool) string {
	groupPath := groupPathOf(group)
	name := artifact + "-" + version + "." + ext
	local := filepath.Join(tc.Dir, filepath.FromSlash(groupPath), artifact, version, name)
	if ok(local) {
		return local
	}
	// Gradle 缓存复用（按精确版本与文件名，不做全盘扫描）
	if !tc.NoGradle {
		if src := findGradleCachedFile(group, artifact, version, name); src != "" && ok(src) {
			os.MkdirAll(filepath.Dir(local), 0755)
			if copyFile(src, local) == nil {
				return local
			}
			return src
		}
	}
	if err := tc.download(groupPath, artifact, version, name, local); err != nil {
		return ""
	}
	return local
}

// download 依次尝试各仓库下载构件到 local
func (tc *Toolchain) download(groupPath, artifact, version, name, local string) error {
	os.MkdirAll(filepath.Dir(local), 0755)
	var lastErr error
	for _, repo := range tc.Repos {
		url := fmt.Sprintf("%s/%s/%s/%s/%s", strings.TrimRight(repo, "/"), groupPath, artifact, version, name)
		if err := tc.fetchTo(url, local); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无可用的仓库")
	}
	return lastErr
}

// fetchTo 下载 url 到本地文件（临时文件 + 重命名），带一次重试
func (tc *Toolchain) fetchTo(url, dst string) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := tc.client.Get(url)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
			continue
		}
		tmp := dst + ".tmp"
		out, err := os.Create(tmp)
		if err != nil {
			resp.Body.Close()
			return err
		}
		_, cerr := io.Copy(out, resp.Body)
		out.Close()
		resp.Body.Close()
		if cerr != nil {
			lastErr = cerr
			continue
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
		return nil
	}
	return lastErr
}

// tcPOM 工具链 POM 依赖模型（含 exclusions）
type tcPOM struct {
	Dependencies []tcPOMDep `xml:"dependencies>dependency"`
}

type tcPOMDep struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
	Optional   string `xml:"optional"`
	Exclusions []struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
	} `xml:"exclusions>exclusion"`
}

// hasWildcardExclusion 判断依赖是否声明显式排除全部传递依赖（*:*）
func hasWildcardExclusion(dep tcPOMDep) bool {
	for _, ex := range dep.Exclusions {
		if ex.GroupID == "*" && ex.ArtifactID == "*" {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
