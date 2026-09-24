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

// Parse 解析整个项目的 Gradle 配置
func (g GradleConfigParser) Parse(projectDir string) *ProjectConfig {
	config := &ProjectConfig{
		RootDir:          projectDir,
		Catalog:          NewVersionCatalog(),
		GradleProperties: map[string]string{},
	}

	config.GradleProperties = GradlePropertiesParser{}.Parse(filepath.Join(projectDir, "gradle.properties"))
	config.Catalog = VersionCatalogParser{}.Parse(filepath.Join(projectDir, "gradle", "libs.versions.toml"))

	settingsResult := SettingsParser{Script: g.Script}.Parse(filepath.Join(projectDir, "settings.gradle.kts"))
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
		module := BuildFileParser{Script: g.Script}.Parse(buildFile, config.Catalog, config.GradleProperties)
		config.Modules = append(config.Modules, module)
	}

	return config
}
