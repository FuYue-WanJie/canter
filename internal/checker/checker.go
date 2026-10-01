package checker

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"canter/internal/mirror"
	"canter/internal/parser"
)

// CheckResult 检测结果
type CheckResult struct {
	Name                string
	Found               bool
	Path                *string
	Version             *string
	Message             string
	DownloadURL         *string
	InstallInstructions *string
	Category            string // tool, sdk, jdk, dependency
}

// MissingDependency 缺失依赖
type MissingDependency struct {
	Group    string
	Artifact string
	Version  string
	Scope    string
}

var javaVersionRe = regexp.MustCompile(`version "(\d+)`)

// ToolchainChecker 工具链检测器
type ToolchainChecker struct {
	AndroidSDK    string
	MirrorManager *mirror.Manager
	Results       []CheckResult
	MissingDeps   []MissingDependency
}

// NewToolchainChecker 创建检测器
func NewToolchainChecker(androidSDK string) *ToolchainChecker {
	if androidSDK == "" {
		androidSDK = os.Getenv("ANDROID_SDK_ROOT")
		if androidSDK == "" {
			home, _ := os.UserHomeDir()
			androidSDK = filepath.Join(home, ".android-sdk")
		}
	}
	return &ToolchainChecker{
		AndroidSDK:    androidSDK,
		MirrorManager: mirror.NewManager(""),
	}
}

// CheckAll 执行全部检查
func (c *ToolchainChecker) CheckAll(config *parser.ProjectConfig) bool {
	c.Results = nil
	c.MissingDeps = nil
	c.CheckJava(config)
	c.CheckAndroidSDK(config)
	c.CheckBuildTools(config)
	c.CheckPlatform(config)
	c.CheckKotlinCompiler(config)
	c.CheckDependencies(config)

	for _, r := range c.Results {
		if r.Category != "dependency" && !r.Found {
			return false
		}
	}
	return true
}

// CheckJava 检测 Java
func (c *ToolchainChecker) CheckJava(config *parser.ProjectConfig) CheckResult {
	result := CheckResult{Name: "java", Category: "jdk"}
	var javaBin string
	var javaVersion string

	javaHome := os.Getenv("JAVA_HOME")
	if javaHome != "" {
		candidate := filepath.Join(javaHome, "bin", "java")
		if _, err := os.Stat(candidate); err == nil {
			javaBin = candidate
		}
	}
	if javaBin == "" {
		if prop := config.GradleProperties["org.gradle.java.home"]; prop != "" {
			path := strings.ReplaceAll(prop, "\\", "/")
			candidate := filepath.Join(path, "bin", "java")
			if _, err := os.Stat(candidate); err == nil {
				javaBin = candidate
			}
		}
	}
	if javaBin == "" {
		if p, err := exec.LookPath("java"); err == nil {
			javaBin = p
		}
	}

	if javaBin != "" {
		if _, err := os.Stat(javaBin); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, javaBin, "-version")
			output, _ := cmd.CombinedOutput()
			if m := javaVersionRe.FindStringSubmatch(string(output)); m != nil {
				javaVersion = m[1]
			}
		}
	}

	requiredVersion := "17"
	for _, mod := range config.Modules {
		if mod.Android != nil && mod.Android.JvmTarget != "" {
			requiredVersion = extractDigits(mod.Android.JvmTarget)
			break
		}
	}

	found := javaBin != ""
	if _, err := os.Stat(javaBin); err != nil {
		found = false
	}
	versionOK := true
	if found && javaVersion != "" {
		major, err1 := strconv.Atoi(javaVersion)
		req, err2 := strconv.Atoi(requiredVersion)
		if err1 == nil && err2 == nil {
			versionOK = major >= req
		}
	}
	result.Found = found && versionOK

	if javaVersion == "" {
		result.Message = fmt.Sprintf("Java 未知 (需要 >= %s)", requiredVersion)
	} else {
		result.Message = fmt.Sprintf("Java %s (需要 >= %s)", javaVersion, requiredVersion)
	}
	if javaBin != "" {
		result.Path = &javaBin
	}

	if !result.Found && javaBin == "" {
		arch := "x64"
		if runtime.GOARCH != "amd64" {
			arch = "aarch64"
		}
		url := fmt.Sprintf("https://mirrors.tuna.tsinghua.edu.cn/Adoptium/%s/jdk/%s/linux/", requiredVersion, arch)
		result.DownloadURL = &url
		instructions := strings.Join([]string{
			fmt.Sprintf("安装 Java %s (推荐清华 Adoptium 镜像):", requiredVersion),
			"  # 方式 1: apt（最快）",
			fmt.Sprintf("  apt-get install -y openjdk-%s-jdk-headless", requiredVersion),
			"",
			"  # 方式 2: 手动下载（清华镜像）",
			"  ARCH=$(uname -m)",
			`  if [ "$ARCH" = "x86_64" ]; then ARCH=x64; else ARCH=aarch64; fi`,
			fmt.Sprintf("  curl -L \"%s\" -o jdk.tar.gz", url),
			"  tar xzf jdk.tar.gz -C /usr/local",
			fmt.Sprintf("  export JAVA_HOME=/usr/local/jdk-%s", requiredVersion),
		}, "\n")
		result.InstallInstructions = &instructions
	}

	c.Results = append(c.Results, result)
	return result
}

var secondNano = int64(1e9)

// CheckAndroidSDK 检测 Android SDK
func (c *ToolchainChecker) CheckAndroidSDK(config *parser.ProjectConfig) CheckResult {
	result := CheckResult{Name: "android-sdk", Category: "sdk"}
	sdkPath := c.AndroidSDK
	if _, err := os.Stat(sdkPath); err != nil {
		sdkPath = ""
	}
	if sdkPath == "" {
		if prop := config.GradleProperties["sdk.dir"]; prop != "" {
			if _, err := os.Stat(prop); err == nil {
				sdkPath = prop
			}
		}
	}
	if sdkPath == "" {
		propsFile := filepath.Join(config.RootDir, "local.properties")
		if f, err := os.Open(propsFile); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "sdk.dir=") {
					val := strings.SplitN(line, "=", 2)[1]
					if _, err := os.Stat(val); err == nil {
						sdkPath = val
					}
				}
			}
			f.Close()
		}
	}

	result.Found = sdkPath != ""
	if result.Found {
		result.Path = &sdkPath
		result.Message = "Android SDK: " + sdkPath
	} else {
		result.Message = "未找到 Android SDK"
		sdkDownloadURL := "https://mirrors.cloud.tencent.com/AndroidSDK/commandlinetools-linux-11076708_latest.zip"
		result.DownloadURL = &sdkDownloadURL
		instructions := strings.Join([]string{
			"安装 Android SDK（推荐腾讯镜像，唯一可用国内源）:",
			"  # 下载 command-line-tools",
			fmt.Sprintf("  curl -L \"%s\" -o sdk-tools.zip", sdkDownloadURL),
			"  mkdir -p ~/.android-sdk/cmdline-tools/latest",
			"  unzip -q sdk-tools.zip -d ~/.android-sdk/cmdline-tools/latest",
			"  export ANDROID_SDK_ROOT=~/.android-sdk",
			"",
			"  # 安装 platforms 和 build-tools",
			"  yes | ~/.android-sdk/cmdline-tools/latest/bin/sdkmanager \\",
			"      --licenses > /dev/null 2>&1",
			"  ~/.android-sdk/cmdline-tools/latest/bin/sdkmanager \\",
			`      "platform-tools" "platforms;android-35" "build-tools;35.0.0"`,
		}, "\n")
		result.InstallInstructions = &instructions
	}
	c.Results = append(c.Results, result)
	return result
}

// CheckBuildTools 检测 build-tools
func (c *ToolchainChecker) CheckBuildTools(config *parser.ProjectConfig) CheckResult {
	result := CheckResult{Name: "build-tools", Category: "sdk"}
	compileSDK := resolveCompileSDK(config)
	btVersion := buildToolsVersionFor(compileSDK)
	btDir := filepath.Join(c.AndroidSDK, "build-tools", btVersion)

	var available []string
	if entries, err := os.ReadDir(filepath.Join(c.AndroidSDK, "build-tools")); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				available = append(available, e.Name())
			}
		}
	}

	_, statErr := os.Stat(btDir)
	result.Found = statErr == nil
	if !result.Found {
		// 与 builder 一致的回退：优先同 major 前缀，否则最高可用版本
		if resolved, ok := resolveBuildToolsDir(c.AndroidSDK, compileSDK, available); ok {
			btDir = resolved
			btVersion = filepath.Base(resolved)
			result.Version = &btVersion
			result.Found = true
			result.Message = "Build Tools " + btVersion + "（compileSdk " + compileSDK + " 的默认版本不可用，已回退）"
			c.Results = append(c.Results, result)
			return result
		}
	}
	result.Version = &btVersion

	if result.Found {
		result.Message = "Build Tools " + btVersion
	} else {
		if len(available) > 0 {
			result.Message = fmt.Sprintf("Build Tools %s 未找到，可用: %s", btVersion, strings.Join(available, ", "))
		} else {
			result.Message = "Build Tools " + btVersion + " 未找到"
		}
		url := fmt.Sprintf("https://mirrors.cloud.tencent.com/AndroidSDK/build-tools_r%s_linux.zip",
			strings.ReplaceAll(strings.ReplaceAll(btVersion, ".", "_"), "-", "_"))
		result.DownloadURL = &url
		instructions := fmt.Sprintf("安装 Build Tools %s:\n  ~/.android-sdk/cmdline-tools/latest/bin/sdkmanager \"build-tools;%s\"",
			btVersion, btVersion)
		result.InstallInstructions = &instructions
	}
	c.Results = append(c.Results, result)
	return result
}

// resolveCompileSDK 取项目中声明的 compileSdk，缺省 35。
func resolveCompileSDK(config *parser.ProjectConfig) string {
	if config != nil {
		for _, mod := range config.Modules {
			if mod.Android != nil && mod.Android.CompileSDK != nil {
				return formatCompileSDK(*mod.Android.CompileSDK)
			}
		}
	}
	return "35"
}

// buildToolsVersionFor 由 compileSdk 推导默认 build-tools 版本。
// 整型（35）-> "35.0.0"；带小版本（36.1/37.1）-> "36.1.0"/"37.1.0"。
func buildToolsVersionFor(compileSDK string) string {
	if strings.Contains(compileSDK, ".") {
		return compileSDK + ".0"
	}
	return compileSDK + ".0.0"
}

// resolveBuildToolsDir 在已安装 build-tools 中回退选择：同 major 前缀优先，否则最高版本。
func resolveBuildToolsDir(sdkRoot, compileSDK string, available []string) (string, bool) {
	if len(available) == 0 {
		return "", false
	}
	major := strings.SplitN(compileSDK, ".", 2)[0]
	sorted := append([]string(nil), available...)
	sort.Strings(sorted)
	highest := sorted[len(sorted)-1]
	for _, name := range sorted {
		if strings.HasPrefix(name, major) {
			return filepath.Join(sdkRoot, "build-tools", name), true
		}
	}
	return filepath.Join(sdkRoot, "build-tools", highest), true
}

// CheckPlatform 检测 platform
func (c *ToolchainChecker) CheckPlatform(config *parser.ProjectConfig) CheckResult {
	result := CheckResult{Name: "platform", Category: "sdk"}
	compileSDK := resolveCompileSDK(config)
	platformDir := filepath.Join(c.AndroidSDK, "platforms", "android-"+compileSDK)
	jarPath := filepath.Join(platformDir, "android.jar")

	var available []string
	if entries, err := os.ReadDir(filepath.Join(c.AndroidSDK, "platforms")); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				available = append(available, e.Name())
			}
		}
	}

	_, jarErr := os.Stat(jarPath)
	result.Found = jarErr == nil
	result.Version = &compileSDK

	if !result.Found {
		// 与 builder 一致的候选回退：android-<sdk> / <sdk>.0 / <major>.0 / <major>
		major := strings.SplitN(compileSDK, ".", 2)[0]
		for _, cand := range []string{"android-" + compileSDK, "android-" + compileSDK + ".0", "android-" + major + ".0", "android-" + major} {
			if info, err := os.Stat(filepath.Join(c.AndroidSDK, "platforms", cand, "android.jar")); err == nil && !info.IsDir() {
				result.Found = true
				result.Message = fmt.Sprintf("Platform %s（compileSdk %s 的平台目录不存在，已回退）", cand, compileSDK)
				c.Results = append(c.Results, result)
				return result
			}
		}
	}

	if result.Found {
		result.Message = "Platform android-" + compileSDK
	} else {
		if len(available) > 0 {
			result.Message = fmt.Sprintf("Platform android-%s 未找到，可用: %s", compileSDK, strings.Join(available, ", "))
		} else {
			result.Message = "Platform android-" + compileSDK + " 未找到"
		}
		url := fmt.Sprintf("https://mirrors.cloud.tencent.com/AndroidSDK/platform-%s_r02.zip", compileSDK)
		result.DownloadURL = &url
		instructions := fmt.Sprintf("安装 Platform android-%s:\n  ~/.android-sdk/cmdline-tools/latest/bin/sdkmanager \"platforms;android-%s\"",
			compileSDK, compileSDK)
		result.InstallInstructions = &instructions
	}
	c.Results = append(c.Results, result)
	return result
}

// CheckKotlinCompiler 检测 Kotlin 编译器
func (c *ToolchainChecker) CheckKotlinCompiler(config *parser.ProjectConfig) CheckResult {
	result := CheckResult{Name: "kotlin-compiler", Category: "tool"}
	var kotlinc string
	if p, err := exec.LookPath("kotlinc"); err == nil {
		kotlinc = p
	} else {
		home, _ := os.UserHomeDir()
		// 优先自有工具链目录（纯镜像构建时使用）
		kotlinc = findFileUnder(filepath.Join(home, ".canter", "toolchain"),
			"kotlin-compiler-embeddable-", ".jar")
		// 回退 Gradle 缓存（可用 CANTER_NO_GRADLE_CACHE=1 禁用）
		if kotlinc == "" && os.Getenv("CANTER_NO_GRADLE_CACHE") == "" {
			kotlinc = findFileUnder(filepath.Join(home, ".gradle", "caches"),
				"kotlin-compiler-embeddable-", ".jar")
		}
	}

	kotlinVersion := config.Catalog.Versions["kotlin"]
	result.Found = kotlinc != ""
	if result.Found {
		if kotlinVersion == "" {
			result.Message = "Kotlin 编译器 (版本未知)"
		} else {
			result.Message = fmt.Sprintf("Kotlin 编译器 (%s)", kotlinVersion)
		}
		result.Path = &kotlinc
	} else {
		result.Message = "未找到 Kotlin 编译器"
		url := fmt.Sprintf("https://github.com/JetBrains/kotlin/releases/download/v%s/kotlin-compiler-%s.zip", kotlinVersion, kotlinVersion)
		if proxy := c.MirrorManager.GitHubProxy(); proxy != "" {
			url = proxy + "/" + url
		}
		result.DownloadURL = &url
		ver := kotlinVersion
		if ver == "" {
			ver = "latest"
		}
		instructions := strings.Join([]string{
			fmt.Sprintf("安装 Kotlin %s:", ver),
			"  # 方式 1: SDKMAN",
			fmt.Sprintf("  sdk install kotlin %s", kotlinVersion),
			"",
			"  # 方式 2: 手动下载",
			fmt.Sprintf("  curl -L \"%s\" -o kotlin.zip", url),
			"  unzip -q kotlin.zip -d ~/.local",
			"  export PATH=$HOME/.local/bin:$PATH",
		}, "\n")
		result.InstallInstructions = &instructions
	}
	c.Results = append(c.Results, result)
	return result
}

// CheckDependencies 检测依赖缓存
func (c *ToolchainChecker) CheckDependencies(config *parser.ProjectConfig) []MissingDependency {
	home, _ := os.UserHomeDir()
	gradleCache := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1")
	mbCache := filepath.Join(home, ".canter", "cache", "deps")

	var missing []MissingDependency
	for _, mod := range config.Modules {
		for _, dep := range mod.Dependencies {
			if dep.IsProject {
				continue
			}
			if dep.Group == "" || dep.Artifact == "" {
				continue
			}
			if dep.Version == "" || dep.Version == "unknown" {
				continue
			}
			gradlePath := filepath.Join(gradleCache, dep.Group, dep.Artifact, dep.Version)
			mbPath := filepath.Join(mbCache, strings.ReplaceAll(dep.Group, ".", "/"), dep.Artifact, dep.Version)
			if os.Getenv("CANTER_NO_GRADLE_CACHE") == "" {
				if _, err := os.Stat(gradlePath); err == nil {
					continue
				}
			}
			if _, err := os.Stat(mbPath); err == nil {
				continue
			}
			missing = append(missing, MissingDependency{
				Group: dep.Group, Artifact: dep.Artifact, Version: dep.Version, Scope: dep.Scope,
			})
		}
	}
	c.MissingDeps = missing

	result := CheckResult{
		Name:     "dependencies",
		Found:    len(missing) == 0,
		Category: "dependency",
	}
	if len(missing) > 0 {
		result.Message = fmt.Sprintf("依赖检查: %d 个缺失", len(missing))
	} else {
		result.Message = "所有依赖已缓存"
	}
	c.Results = append(c.Results, result)
	return missing
}

// ShowReport 打印检测报告
func (c *ToolchainChecker) ShowReport() bool {
	separator := strings.Repeat("=", 70)
	fmt.Println()
	fmt.Println(separator)
	fmt.Println("  工具链检查报告")
	fmt.Println(separator)

	allOK := true
	for _, r := range c.Results {
		if !r.Found {
			allOK = false
		}
		if r.Found {
			fmt.Printf("[OK]   %s: %s\n", r.Name, r.Message)
		} else {
			fmt.Printf("[缺失] %s: %s\n", r.Name, r.Message)
		}
		if r.Path != nil {
			fmt.Printf("  路径: %s\n", *r.Path)
		}
		if !r.Found {
			if r.DownloadURL != nil {
				fmt.Printf("  下载地址: %s\n", *r.DownloadURL)
			}
			if r.InstallInstructions != nil {
				fmt.Println(*r.InstallInstructions)
			}
		}
	}

	if len(c.MissingDeps) > 0 {
		fmt.Printf("缺失依赖 (%d 个):\n", len(c.MissingDeps))
		shown := c.MissingDeps
		if len(shown) > 10 {
			shown = shown[:10]
		}
		for _, d := range shown {
			fmt.Printf("  %s: %s:%s:%s\n", d.Scope, d.Group, d.Artifact, d.Version)
		}
		if len(c.MissingDeps) > 10 {
			fmt.Printf("  ... 还有 %d 个\n", len(c.MissingDeps)-10)
		}
	}

	fmt.Println(separator)
	if allOK && len(c.MissingDeps) == 0 {
		fmt.Println("所有工具链就绪! ✓")
	} else {
		fmt.Println("部分工具缺失，请按上述提示安装")
	}
	fmt.Println(separator)
	return allOK && len(c.MissingDeps) == 0
}

// SuggestFixCommands 生成修复命令
func (c *ToolchainChecker) SuggestFixCommands() []string {
	var commands []string
	for _, r := range c.Results {
		if r.Found {
			continue
		}
		switch r.Name {
		case "java":
			commands = append(commands, "apt-get install -y openjdk-17-jdk-headless")
		case "android-sdk":
			commands = append(commands, "curl -L https://mirrors.cloud.tencent.com/AndroidSDK/commandlinetools-linux-11076708_latest.zip -o sdk.zip && unzip -q sdk.zip -d ~/.android-sdk")
		case "build-tools":
			if r.Version != nil {
				commands = append(commands, fmt.Sprintf("sdkmanager \"build-tools;%s\"", *r.Version))
			}
		case "platform":
			if r.Version != nil {
				commands = append(commands, fmt.Sprintf("sdkmanager \"platforms;android-%s\"", *r.Version))
			}
		}
	}
	return commands
}

// findFileUnder 在目录下递归查找首个名字以 prefix 开头、以 suffix 结尾的文件
func findFileUnder(root, prefix, suffix string) string {
	var found string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || found != "" || info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) &&
			!strings.Contains(name, "sources") && !strings.Contains(name, "javadoc") {
			found = path
		}
		return nil
	})
	return found
}

func formatCompileSDK(sdk float64) string {
	return strconv.FormatFloat(sdk, 'f', -1, 64)
}

func extractDigits(s string) string {
	re := regexp.MustCompile(`\d+`)
	if m := re.FindString(s); m != "" {
		return m
	}
	return s
}
