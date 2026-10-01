package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ConvertResult 描述一次转换的结果。
type ConvertResult struct {
	From        string   // "gradle" 或 "canter"
	To          string   // "canter" 或 "gradle"
	Written     []string // 写入的文件相对路径
	ProjectName string
}

// ToCanter 生成 canter.toml（供 --to-canter 使用）。
// 优先从 Gradle 配置解析转换；若项目没有 Gradle 配置，则规范化现有 canter.toml。
// 返回写出的文件路径。
func ToCanter(projectDir string, force bool) (*ConvertResult, error) {
	path := filepath.Join(projectDir, CanterFileName)
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("%s 已存在，使用 --force 覆盖", path)
		}
	}
	var (
		config *ProjectConfig
		source string
	)
	if hasGradleConfig(projectDir) {
		config = GradleConfigParser{}.parseGradle(projectDir)
		source = "gradle"
	} else if c, ok := ParseCanterConfig(projectDir); ok {
		config = c
		source = "canter"
	} else {
		return nil, fmt.Errorf("未找到 Gradle 配置或 %s，无内容可转换", CanterFileName)
	}
	if err := os.WriteFile(path, []byte(WriteCanterConfig(config)), 0644); err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(projectDir, path)
	return &ConvertResult{From: source, To: "canter", Written: []string{filepath.ToSlash(rel)}, ProjectName: config.ProjectName}, nil
}

// hasGradleConfig 判断项目是否存在 Gradle settings 文件。
func hasGradleConfig(projectDir string) bool {
	for _, name := range []string{"settings.gradle.kts", "settings.gradle"} {
		if _, err := os.Stat(filepath.Join(projectDir, name)); err == nil {
			return true
		}
	}
	return false
}

// ToGradle 从 canter.toml 生成 Gradle 配置文件集合（settings/build.gradle.kts/libs.versions.toml）。
func ToGradle(projectDir string, force bool) (*ConvertResult, error) {
	config, ok := ParseCanterConfig(projectDir)
	if !ok {
		return nil, fmt.Errorf("未找到 %s，无法转换为 Gradle 配置", CanterFileName)
	}
	files := GenerateGradleFiles(config)
	// 预检冲突
	if !force {
		for rel := range files {
			p := filepath.Join(projectDir, filepath.FromSlash(rel))
			if _, err := os.Stat(p); err == nil {
				return nil, fmt.Errorf("%s 已存在，使用 --force 覆盖", p)
			}
		}
	}
	var written []string
	for _, rel := range sortedFileKeys(files) {
		p := filepath.Join(projectDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(files[rel]), 0644); err != nil {
			return nil, err
		}
		written = append(written, rel)
	}
	return &ConvertResult{From: "canter", To: "gradle", Written: written, ProjectName: config.ProjectName}, nil
}

func sortedFileKeys(m GradleFiles) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
