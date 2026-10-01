package parser

import (
	"os"
	"path/filepath"
	"strings"
)

// GradleConfigParser 主配置解析器
type GradleConfigParser struct {
	Script GradleScriptParser
}

// LoadProject 加载项目配置，并返回实际使用的来源（"canter" 或 "gradle"）。
// 若项目根目录存在 canter.toml，则优先使用 Canter 原生配置（可与 Gradle 配置共存）；
// 否则回退到静态解析 Gradle 配置。
func LoadProject(projectDir string) (*ProjectConfig, string) {
	if config, ok := ParseCanterConfig(projectDir); ok {
		return config, "canter"
	}
	return GradleConfigParser{}.parseGradle(projectDir), "gradle"
}

// Parse 解析项目配置（canter.toml 优先，回退 Gradle）。
func (g GradleConfigParser) Parse(projectDir string) *ProjectConfig {
	config, _ := LoadProject(projectDir)
	return config
}

// parseGradle 静态解析 Gradle 工程配置。
func (g GradleConfigParser) parseGradle(projectDir string) *ProjectConfig {
	Script := g.Script
	config := &ProjectConfig{
		RootDir:          projectDir,
		Catalog:          NewVersionCatalog(),
		GradleProperties: map[string]string{},
	}

	config.GradleProperties = GradlePropertiesParser{}.Parse(filepath.Join(projectDir, "gradle.properties"))
	config.Catalog = VersionCatalogParser{}.Parse(filepath.Join(projectDir, "gradle", "libs.versions.toml"))

	settingsResult := SettingsParser{Script: Script}.Parse(filepath.Join(projectDir, "settings.gradle.kts"))
	config.ProjectName = settingsResult.ProjectName
	repos := settingsResult.Repositories
	pluginRepos := settingsResult.PluginRepositories

	if len(repos) == 0 {
		repos = []string{"https://dl.google.com/dl/android/maven2", "https://repo.maven.apache.org/maven2"}
	}
	if len(pluginRepos) == 0 {
		pluginRepos = []string{"https://plugins.gradle.org/maven2", "https://dl.google.com/dl/android/maven2", "https://repo.maven.apache.org/maven2"}
	}
	config.Repositories = repos
	config.PluginRepositories = pluginRepos

	for _, inc := range settingsResult.Includes {
		moduleRel := strings.TrimLeft(inc, ":")
		moduleRel = strings.ReplaceAll(moduleRel, ":", "/")
		moduleDir := filepath.Join(projectDir, filepath.FromSlash(moduleRel))
		buildFile := filepath.Join(moduleDir, "build.gradle.kts")
		if _, err := os.Stat(buildFile); err != nil {
			buildFile = filepath.Join(moduleDir, "build.gradle")
			if _, err := os.Stat(buildFile); err != nil {
				continue
			}
		}
		module := BuildFileParser{Script: Script}.Parse(buildFile, config.Catalog, config.GradleProperties)
		config.Modules = append(config.Modules, module)
	}

	// 处理 composite build（includeBuild）：解析其 settings 并将模块纳入（供编译其类）
	for _, ib := range settingsResult.IncludeBuilds {
		ibDir := filepath.Join(projectDir, filepath.FromSlash(strings.TrimLeft(ib, "./")))
		ibSettingsPath := filepath.Join(ibDir, "settings.gradle.kts")
		if _, err := os.Stat(ibSettingsPath); err != nil {
			ibSettingsPath = filepath.Join(ibDir, "settings.gradle")
		}
		ibSettings := SettingsParser{Script: Script}.Parse(ibSettingsPath)
		for _, inc := range ibSettings.Includes {
			rel := strings.TrimLeft(inc, ":")
			rel = strings.ReplaceAll(rel, ":", "/")
			modDir := filepath.Join(ibDir, filepath.FromSlash(rel))
			if remap, ok := ibSettings.ProjectDirRemap[inc]; ok {
				modDir = filepath.Join(ibDir, filepath.FromSlash(strings.TrimLeft(remap, "./")))
			}
			buildFile := filepath.Join(modDir, "build.gradle.kts")
			if _, err := os.Stat(buildFile); err != nil {
				buildFile = filepath.Join(modDir, "build.gradle")
				if _, err := os.Stat(buildFile); err != nil {
					continue
				}
			}
			module := BuildFileParser{Script: Script}.Parse(buildFile, config.Catalog, config.GradleProperties)
			module.IncludeBuild = filepath.ToSlash(strings.TrimLeft(ib, "./"))
			config.Modules = append(config.Modules, module)
		}
	}

	return config
}
