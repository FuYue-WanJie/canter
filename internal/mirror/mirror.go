package mirror

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mirror 镜像源
type Mirror struct {
	Name           string
	URL            string
	Description    string
	Region         string
	IsGoogle       bool
	IsPluginPortal bool
	IsGradleDist   bool
	IsAndroidSDK   bool
	IsJDK          bool
}

const GitHubProxy = "https://gh-proxy.org/"

var MavenMirrors = []Mirror{
	{"maven-central", "https://repo.maven.apache.org/maven2", "Maven Central 官方", "US", false, false, false, false, false},
	{"aliyun", "https://maven.aliyun.com/repository/central", "阿里云 Central", "CN", false, false, false, false, false},
	{"tencent", "https://mirrors.cloud.tencent.com/nexus/repository/maven-public", "腾讯云 Nexus", "CN", false, false, false, false, false},
	{"huawei", "https://mirrors.huaweicloud.com/repository/maven", "华为云 Maven", "CN", false, false, false, false, false},
	{"nju", "https://repo.nju.edu.cn/maven", "南京大学直连", "CN", false, false, false, false, false},
	{"cernet", "https://mirrors.cernet.edu.cn/maven", "教育网联合站 (MirrorZ)", "CN", false, false, false, false, false},
}

var GoogleMirrors = []Mirror{
	{"google", "https://dl.google.com/dl/android/maven2", "Google Maven 官方", "US", true, false, false, false, false},
	{"aliyun-google", "https://maven.aliyun.com/repository/google", "阿里云 Google 镜像", "CN", true, false, false, false, false},
	{"tencent-google", "https://mirrors.cloud.tencent.com/nexus/repository/maven-public", "腾讯云", "CN", true, false, false, false, false},
	{"huawei-google", "https://mirrors.huaweicloud.com/repository/maven", "华为云", "CN", true, false, false, false, false},
	{"nju-google", "https://repo.nju.edu.cn/maven", "南京大学直连", "CN", true, false, false, false, false},
	{"cernet-google", "https://mirrors.cernet.edu.cn/maven", "教育网联合站", "CN", true, false, false, false, false},
}

var PluginMirrors = []Mirror{
	{"gradle-plugins", "https://plugins.gradle.org/maven2", "Gradle 插件门户官方", "US", false, true, false, false, false},
	{"aliyun-plugins", "https://maven.aliyun.com/repository/gradle-plugin", "阿里云插件", "CN", false, true, false, false, false},
	{"huawei-plugins", "https://mirrors.huaweicloud.com/repository/maven", "华为云", "CN", false, true, false, false, false},
	{"nju-plugins", "https://repo.nju.edu.cn/maven", "南京大学直连", "CN", false, true, false, false, false},
	{"cernet-plugins", "https://mirrors.cernet.edu.cn/maven", "教育网联合站", "CN", false, true, false, false, false},
}

var GradleDistMirrors = []Mirror{
	{"tencent-gradle-dist", "https://mirrors.cloud.tencent.com/gradle", "腾讯云 Gradle", "CN", false, false, true, false, false},
	{"huawei-gradle-dist", "https://mirrors.huaweicloud.com/gradle", "华为云 Gradle", "CN", false, false, true, false, false},
	{"nju-gradle-dist", "https://mirror.nju.edu.cn/gradle", "南京大学 Gradle", "CN", false, false, true, false, false},
	{"cernet-gradle-dist", "https://mirrors.cernet.edu.cn/gradle", "教育网联合站", "CN", false, false, true, false, false},
}

var SDKMirrors = []Mirror{
	{"tencent-sdk", "https://mirrors.cloud.tencent.com/AndroidSDK", "腾讯云 Android SDK", "CN", false, false, false, true, false},
}

var JDKMirrors = []Mirror{
	{"tuna-adoptium", "https://mirrors.tuna.tsinghua.edu.cn/Adoptium", "清华 Adoptium", "CN", false, false, false, false, true},
}

var JitpackMirror = Mirror{"jitpack", "https://jitpack.io", "JitPack（无镜像）", "US", false, false, false, false, false}

// 测速使用的测试文件（越小越快）
var testFiles = map[string]string{
	"google_maven":  "androidx/core/core/1.0.0/core-1.0.0.pom",
	"maven_central": "commons-io/commons-io/2.0.1/commons-io-2.0.1.pom",
	"plugin_portal": "org/gradle/gradle-tooling-api/1.0-milestone-1/gradle-tooling-api-1.0-milestone-1.pom",
}

// Combo 预置镜像组合
type Combo struct {
	ID          string
	Name        string
	Description string
	GradleDist  string
	Maven       string
	Google      string
	Plugins     string
}

var RecommendedCombos = []Combo{
	{"tencent-aliyun", "普通宽带（最稳）", "腾讯 Gradle 发行版 + 阿里云三仓 Maven", "tencent-gradle-dist", "aliyun", "aliyun-google", "aliyun-plugins"},
	{"huawei-allinone", "省事（华为单仓）", "华为一个地址全覆盖 Maven/Google/插件（实测最快 ~37MB/s）", "huawei-gradle-dist", "huawei", "huawei-google", "huawei-plugins"},
	{"cernet-auto", "教育网（联合站自动路由）", "CERNET 自动路由到南大/成员站，教育网内速度最优", "cernet-gradle-dist", "cernet", "cernet-google", "cernet-plugins"},
}

// SpeedTestResult 测速结果
type SpeedTestResult struct {
	Mirror    Mirror
	LatencyMS float64 // -1 表示失败
	Success   bool
}

// String 格式化测速结果
func (r SpeedTestResult) String() string {
	if r.Success {
		return fmt.Sprintf("%-25s: %6.0fms", r.Mirror.Name, r.LatencyMS)
	}
	return fmt.Sprintf("%-25s: ✗ 失败", r.Mirror.Name)
}

// Manager 镜像源管理器
type Manager struct {
	ConfigDir          string
	ConfigFile         string
	SelectedMaven      string
	SelectedGoogle     string
	SelectedPlugins    string
	SelectedGradleDist string
	SelectedSDKMirror  string
}

// NewManager 创建镜像管理器
func NewManager(configDir string) *Manager {
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".minibuild")
	}
	m := &Manager{
		ConfigDir:  configDir,
		ConfigFile: filepath.Join(configDir, "mirrors.json"),
	}
	os.MkdirAll(configDir, 0755)
	m.loadConfig()
	return m
}

func (m *Manager) loadConfig() {
	data, err := os.ReadFile(m.ConfigFile)
	if err != nil {
		return
	}
	var cfg map[string]string
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}
	m.SelectedMaven = cfg["maven"]
	m.SelectedGoogle = cfg["google"]
	m.SelectedPlugins = cfg["plugins"]
	m.SelectedGradleDist = cfg["gradle_dist"]
	m.SelectedSDKMirror = cfg["sdk"]
}

func (m *Manager) saveConfig() {
	cfg := map[string]string{
		"maven":       m.SelectedMaven,
		"google":      m.SelectedGoogle,
		"plugins":     m.SelectedPlugins,
		"gradle_dist": m.SelectedGradleDist,
		"sdk":         m.SelectedSDKMirror,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(m.ConfigFile, data, 0644)
}

// ListAll 列出全部镜像
func (m *Manager) ListAll() []Mirror {
	var all []Mirror
	all = append(all, MavenMirrors...)
	all = append(all, GoogleMirrors...)
	all = append(all, PluginMirrors...)
	all = append(all, GradleDistMirrors...)
	all = append(all, SDKMirrors...)
	all = append(all, JDKMirrors...)
	all = append(all, JitpackMirror)
	return all
}

// ListMaven 列出 Maven 镜像
func (m *Manager) ListMaven() []Mirror { return MavenMirrors }

// ListGoogle 列出 Google 镜像
func (m *Manager) ListGoogle() []Mirror { return GoogleMirrors }

// ListPlugins 列出插件镜像
func (m *Manager) ListPlugins() []Mirror { return PluginMirrors }

// ListGradleDist 列出 Gradle 发行版镜像
func (m *Manager) ListGradleDist() []Mirror { return GradleDistMirrors }

// ListSDK 列出 SDK 镜像
func (m *Manager) ListSDK() []Mirror { return SDKMirrors }

// ListJDK 列出 JDK 镜像
func (m *Manager) ListJDK() []Mirror { return JDKMirrors }

// GitHubProxy 返回 GitHub 代理前缀
func (m *Manager) GitHubProxy() string { return GitHubProxy }

func (m *Manager) selectMaven(name string) bool {
	for _, mr := range MavenMirrors {
		if mr.Name == name {
			m.SelectedMaven = name
			m.saveConfig()
			return true
		}
	}
	return false
}

func (m *Manager) selectGoogle(name string) bool {
	for _, mr := range GoogleMirrors {
		if mr.Name == name {
			m.SelectedGoogle = name
			m.saveConfig()
			return true
		}
	}
	return false
}

func (m *Manager) selectPlugins(name string) bool {
	for _, mr := range PluginMirrors {
		if mr.Name == name {
			m.SelectedPlugins = name
			m.saveConfig()
			return true
		}
	}
	return false
}

func (m *Manager) selectGradleDist(name string) bool {
	for _, mr := range GradleDistMirrors {
		if mr.Name == name {
			m.SelectedGradleDist = name
			m.saveConfig()
			return true
		}
	}
	return false
}

// SelectCombo 选择预置组合
func (m *Manager) SelectCombo(comboID string) (*Combo, bool) {
	for _, c := range RecommendedCombos {
		if c.ID == comboID {
			m.selectMaven(c.Maven)
			m.selectGoogle(c.Google)
			m.selectPlugins(c.Plugins)
			m.selectGradleDist(c.GradleDist)
			return &c, true
		}
	}
	return nil, false
}

// SelectByName 按类型选择镜像
func (m *Manager) SelectByName(name, mirrorType string) bool {
	switch mirrorType {
	case "all", "maven":
		return m.selectMaven(name)
	case "google":
		return m.selectGoogle(name)
	case "plugins":
		return m.selectPlugins(name)
	case "gradle_dist":
		return m.selectGradleDist(name)
	}
	return false
}

// SpeedTest 测速
func (m *Manager) SpeedTest(mirrorType string, timeout float64) []SpeedTestResult {
	var mirrors []Mirror
	var testPath string
	switch mirrorType {
	case "all", "maven":
		mirrors = MavenMirrors
		testPath = testFiles["maven_central"]
	case "google":
		mirrors = GoogleMirrors
		testPath = testFiles["google_maven"]
	case "plugins":
		mirrors = PluginMirrors
		testPath = testFiles["plugin_portal"]
	case "gradle_dist":
		mirrors = GradleDistMirrors
		testPath = "gradle/gradle-8.10.2-bin.zip"
	case "sdk":
		mirrors = SDKMirrors
		testPath = "platform-35_r02.zip"
	default:
		mirrors = m.ListAll()
		testPath = testFiles["maven_central"]
	}

	if timeout <= 0 {
		timeout = 5.0
	}

	results := make([]SpeedTestResult, len(mirrors))
	var wg sync.WaitGroup
	for i, mr := range mirrors {
		wg.Add(1)
		go func(idx int, mirror Mirror) {
			defer wg.Done()
			latency, ok := ping(mirror.URL+"/"+testPath, timeout)
			results[idx] = SpeedTestResult{Mirror: mirror, LatencyMS: latency, Success: ok}
		}(i, mr)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		if results[i].Success != results[j].Success {
			return results[i].Success
		}
		return results[i].LatencyMS < results[j].LatencyMS
	})
	return results
}

func ping(url string, timeout float64) (float64, bool) {
	start := time.Now()
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return -1, false
	}
	req.Header.Set("User-Agent", "MiniBuild/0.1")
	req.Header.Set("Range", "bytes=0-1048575")
	client := &http.Client{Timeout: time.Duration(timeout * float64(time.Second))}
	resp, err := client.Do(req)
	if err != nil {
		return -1, false
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)
	if resp.StatusCode >= 500 {
		return -1, false
	}
	return float64(elapsed.Microseconds()) / 1000.0, true
}

// AutoSelect 自动选择最快镜像
func (m *Manager) AutoSelect(mirrorType string) *Mirror {
	results := m.SpeedTest(mirrorType, 0)
	var okResults []SpeedTestResult
	for _, r := range results {
		if r.Success {
			okResults = append(okResults, r)
		}
	}
	if len(okResults) == 0 {
		fmt.Fprintln(os.Stderr, "所有镜像都不可用")
		return nil
	}
	best := okResults[0]
	fmt.Printf("最快: %s (%0.0fms)\n", best.Mirror.Name, best.LatencyMS)
	m.SelectByName(best.Mirror.Name, mirrorType)
	return &best.Mirror
}

func (m *Manager) find(category string) *Mirror {
	var name string
	switch category {
	case "google":
		name = m.SelectedGoogle
	case "maven":
		name = m.SelectedMaven
	case "plugins":
		name = m.SelectedPlugins
	case "gradle_dist":
		name = m.SelectedGradleDist
	}
	if name == "" {
		return nil
	}
	for _, mr := range m.ListAll() {
		if mr.Name == name {
			return &mr
		}
	}
	return nil
}

// GetRepositories 获取 3 个仓库 URL（Google、Maven Central、插件门户）
func (m *Manager) GetRepositories() []string {
	var repos []string
	if g := m.find("google"); g != nil {
		repos = append(repos, g.URL)
	} else {
		repos = append(repos, "https://dl.google.com/dl/android/maven2")
	}
	if c := m.find("maven"); c != nil {
		repos = append(repos, c.URL)
	} else {
		repos = append(repos, "https://repo.maven.apache.org/maven2")
	}
	if p := m.find("plugins"); p != nil {
		repos = append(repos, p.URL)
	} else {
		repos = append(repos, "https://plugins.gradle.org/maven2")
	}
	return repos
}

// IsSelected 检查镜像是否被选中
func (m *Manager) IsSelected(mr Mirror) bool {
	return mr.Name == m.SelectedMaven || mr.Name == m.SelectedGoogle ||
		mr.Name == m.SelectedPlugins || mr.Name == m.SelectedGradleDist
}

// IsComboSelected 检查组合是否被选中
func (m *Manager) IsComboSelected(comboID string) bool {
	for _, c := range RecommendedCombos {
		if c.ID == comboID {
			return m.SelectedMaven == c.Maven && m.SelectedGoogle == c.Google && m.SelectedPlugins == c.Plugins
		}
	}
	return false
}

// GenerateSettingsKts 生成 settings.gradle.kts 片段
func (m *Manager) GenerateSettingsKts() string {
	repos := m.GetRepositories()
	var sb strings.Builder
	sb.WriteString("pluginManagement {\n")
	sb.WriteString("    repositories {\n")
	for _, r := range repos {
		sb.WriteString(fmt.Sprintf("        maven { url = uri(\"%s\") }\n", r))
	}
	sb.WriteString("    }\n")
	sb.WriteString("}\n\n")
	sb.WriteString("dependencyResolutionManagement {\n")
	sb.WriteString("    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)\n")
	sb.WriteString("    repositories {\n")
	for _, r := range repos {
		sb.WriteString(fmt.Sprintf("        maven { url = uri(\"%s\") }\n", r))
	}
	sb.WriteString("        mavenLocal()\n")
	sb.WriteString("        maven { url = uri(\"https://jitpack.io\") }\n")
	sb.WriteString("    }\n")
	sb.WriteString("}\n")
	return sb.String()
}

// GenerateWrapperProperties 生成 gradle-wrapper.properties 片段
func (m *Manager) GenerateWrapperProperties(gradleVersion string) string {
	return fmt.Sprintf("distributionUrl=https\\://services.gradle.org/distributions/gradle-%s-bin.zip", gradleVersion)
}
