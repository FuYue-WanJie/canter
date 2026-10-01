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

// ToCanter 从项目配置生成 canter.toml（供 --to-canter 使用）。
// 若源为 Gradle，则先静态解析 Gradle；否则读现有 canter.toml。
// 返回写出的文件路径。
func ToCanter(projectDir string, force bool) (*ConvertResult, error) {
	path := filepath.Join(projectDir, CanterFileName)
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("%s 已存在，使用 --force 覆盖", path)
		}
	}
	config, source := LoadProject(projectDir)
	if err := os.WriteFile(path, []byte(WriteCanterConfig(config)), 0644); err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(projectDir, path)
	return &ConvertResult{From: source, To: "canter", Written: []string{filepath.ToSlash(rel)}, ProjectName: config.ProjectName}, nil
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
