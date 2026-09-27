package builder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// gradleModule 表示 Gradle Module Metadata（.module 文件）
type gradleModule struct {
	Variants []gradleVariant `json:"variants"`
}

type gradleVariant struct {
	Name                  string             `json:"name"`
	Attributes            map[string]string  `json:"attributes"`
	Dependencies          []moduleDep        `json:"dependencies"`
	DependencyConstraints []moduleDep        `json:"dependencyConstraints"`
	AvailableAt           *moduleAvailableAt `json:"available-at"`
}

// moduleAvailableAt available-at 重定向：变体的真实内容在另一个模块（KMP 根模块的典型形态）
type moduleAvailableAt struct {
	URL     string `json:"url"`
	Group   string `json:"group"`
	Module  string `json:"module"`
	Version struct {
		Requires string `json:"requires"`
	} `json:"version"`
}

type moduleDep struct {
	Group   string `json:"group"`
	Module  string `json:"module"`
	Reason  string `json:"reason"`
	Version struct {
		Requires string `json:"requires"`
		Strictly string `json:"strictly"`
		Prefer   string `json:"prefer"`
	} `json:"version"`
}

// requiredVersionOf 按优先级取依赖版本：strictly > requires > prefer
func requiredVersionOf(d moduleDep) string {
	if d.Version.Strictly != "" {
		return normalizeVersion(d.Version.Strictly)
	}
	if d.Version.Requires != "" {
		return normalizeVersion(d.Version.Requires)
	}
	return normalizeVersion(d.Version.Prefer)
}

// findGradleModuleFile 在 Gradle 缓存中查找 .module 文件（Gradle 缓存的 group 目录用点号）
func findGradleModuleFile(group, artifact, version string) string {
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
	target := artifact + "-" + version + ".module"
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

// selectModuleVariant 依据 Gradle 变体属性挑选运行时变体。
// 与 Gradle 的 consumer 属性匹配对齐：java-runtime 优先于 java-api，
// androidJvm 平台优先，artifacts/非 sources 变体优先。
func selectModuleVariant(m *gradleModule) *gradleVariant {
	var best *gradleVariant
	bestScore := -1
	score := func(v *gradleVariant) int {
		attrs := v.Attributes
		usage := attrs["org.gradle.usage"]
		platform := attrs["org.jetbrains.kotlin.platform.type"]
		category := attrs["org.gradle.category"]

		s := 0
		switch usage {
		case "java-runtime":
			s += 8
		case "java-api":
			s += 4
		}
		switch platform {
		case "androidJvm":
			s += 6
		case "jvm":
			s += 2
		case "common":
			// common/metadata 变体仅在无平台变体时兜底（不加分）
		}
		if strings.Contains(category, "library") {
			s += 1
		}
		// sources/docs/metadata 变体是辅助产物
		lname := strings.ToLower(v.Name)
		if strings.Contains(lname, "sources") || strings.Contains(lname, "javadoc") ||
			strings.Contains(lname, "metadata") || strings.Contains(lname, "docs") {
			s -= 10
		}
		// 空变体（deps 全部 available-at）无信息量，靠后
		if len(v.Dependencies) == 0 && len(v.DependencyConstraints) == 0 {
			s -= 2
		}
		return s
	}
	for i := range m.Variants {
		v := &m.Variants[i]
		s := score(v)
		if best == nil || s > bestScore {
			best = v
			bestScore = s
		}
	}
	return best
}

// readModuleJSON 读取并解析 .module 文件
func readModuleJSON(path string) (*gradleModule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m gradleModule
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// parseModuleDeps 读取 .module 文件，返回其依赖与约束。
// 变体带 available-at 时跟随重定向到目标模块读取真实依赖。
func (b *Builder) parseModuleDeps(downloader *Downloader, group, artifact, version string) (deps []struct{ group, artifact, version string }, constraints map[string]string) {
	fetch := func(g, a, v string) (string, error) {
		return downloader.FetchModule(g, a, v)
	}
	return parseModuleDepsWithFetcher(fetch, group, artifact, version)
}

// parseModuleDepsWithFetcher 按给定 fetcher 解析 .module 依赖（available-at 递归跟随）
func parseModuleDepsWithFetcher(fetch func(group, artifact, version string) (string, error), group, artifact, version string) (deps []struct{ group, artifact, version string }, constraints map[string]string) {
	path, err := fetch(group, artifact, version)
	if err != nil || path == "" {
		return nil, nil
	}
	m, err := readModuleJSON(path)
	if err != nil {
		return nil, nil
	}
	vari := selectModuleVariant(m)
	if vari == nil {
		return nil, nil
	}

	// available-at：变体真实内容在目标模块（如 lifecycle-runtime-ktx -> lifecycle-runtime-ktx-android）
	if vari.AvailableAt != nil && vari.AvailableAt.Module != "" {
		at := vari.AvailableAt
		ver := normalizeVersion(at.Version.Requires)
		if ver == "" {
			ver = version
		}
		if at.Group != "" && at.Module != "" && !(at.Group == group && at.Module == artifact && ver == version) {
			return parseModuleDepsWithFetcher(fetch, at.Group, at.Module, ver)
		}
	}

	return extractModuleDeps(vari)
}

// extractModuleDeps 从变体中提取依赖与约束（跳过非 Android 平台变体）
func extractModuleDeps(vari *gradleVariant) (deps []struct{ group, artifact, version string }, constraints map[string]string) {
	for _, d := range vari.Dependencies {
		if d.Group == "" || d.Module == "" {
			continue
		}
		v := requiredVersionOf(d)
		if v == "" {
			continue
		}
		if hasSuffix(d.Module, nonAndroidSuffixes) {
			continue
		}
		deps = append(deps, struct{ group, artifact, version string }{d.Group, d.Module, v})
	}
	if len(vari.DependencyConstraints) > 0 {
		constraints = map[string]string{}
		for _, c := range vari.DependencyConstraints {
			if c.Group == "" || c.Module == "" {
				continue
			}
			v := requiredVersionOf(c)
			if v != "" {
				constraints[c.Group+":"+c.Module] = v
			}
		}
	}
	return deps, constraints
}
