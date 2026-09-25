package builder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// gradleModule 表示 Gradle Module Metadata（.module 文件）
type gradleModule struct {
	Variants []struct {
		Name                 string            `json:"name"`
		Attributes           map[string]string `json:"attributes"`
		Dependencies         []moduleDep       `json:"dependencies"`
		DependencyConstraints []moduleDep      `json:"dependencyConstraints"`
	} `json:"variants"`
}

type moduleDep struct {
	Group   string `json:"group"`
	Module  string `json:"module"`
	Version struct {
		Requires string `json:"requires"`
	} `json:"version"`
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

// selectModuleVariant 选择最合适的变体（优先 Android 运行时变体）
func selectModuleVariant(m *gradleModule) *struct {
	Name                 string
	Attributes           map[string]string
	Dependencies         []moduleDep
	DependencyConstraints []moduleDep
} {
	var best *int
	score := func(v int) int {
		vari := m.Variants[v]
		name := strings.ToLower(vari.Name)
		usage := vari.Attributes["org.gradle.usage"]
		platform := vari.Attributes["org.jetbrains.kotlin.platform.type"]
		s := 0
		if strings.Contains(name, "android") {
			s += 4
		}
		if usage == "java-runtime" {
			s += 2
		}
		if platform == "androidJvm" {
			s += 3
		} else if platform == "jvm" {
			s += 1
		}
		if strings.Contains(name, "runtimeelements") {
			s += 1
		}
		return s
	}
	for i := range m.Variants {
		if best == nil || score(i) > score(*best) {
			idx := i
			best = &idx
		}
	}
	if best == nil {
		return nil
	}
	v := m.Variants[*best]
	return &struct {
		Name                 string
		Attributes           map[string]string
		Dependencies         []moduleDep
		DependencyConstraints []moduleDep
	}{v.Name, v.Attributes, v.Dependencies, v.DependencyConstraints}
}

// parseModuleDeps 读取 .module 文件，返回其依赖与约束
func (b *Builder) parseModuleDeps(downloader *Downloader, group, artifact, version string) (deps []struct{ group, artifact, version string }, constraints map[string]string) {
	path, err := downloader.FetchModule(group, artifact, version)
	if err != nil || path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var m gradleModule
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil
	}
	vari := selectModuleVariant(&m)
	if vari == nil {
		return nil, nil
	}
	for _, d := range vari.Dependencies {
		if d.Group == "" || d.Module == "" {
			continue
		}
		v := normalizeVersion(d.Version.Requires)
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
			v := normalizeVersion(c.Version.Requires)
			if v != "" {
				constraints[c.Group+":"+c.Module] = v
			}
		}
	}
	return deps, constraints
}
