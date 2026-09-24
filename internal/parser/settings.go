package parser

import (
	"os"
	"regexp"
	"strings"
)

var (
	rootProjectNameRe = regexp.MustCompile(`rootProject\.name\s*=\s*"([^"]*)"`)
	includeParenRe    = regexp.MustCompile(`include\s*\(([^)]*)\)`)
	includeNoParenRe  = regexp.MustCompile(`include\s+"([^"]*)"`)
	googleRepoRe      = regexp.MustCompile(`^google\s*\(\s*\)`)
	mavenCentralRe    = regexp.MustCompile(`^mavenCentral\s*\(\s*\)`)
	mavenLocalRe      = regexp.MustCompile(`^mavenLocal\s*\(\s*\)`)
	pluginPortalRe    = regexp.MustCompile(`^gradlePluginPortal\s*\(\s*\)`)
	uriRe             = regexp.MustCompile(`uri\s*\(\s*"([^"]*)"\s*\)`)
	urlEqualsRe       = regexp.MustCompile(`url\s*=\s*"([^"]*)"`)
)

// SettingsParser settings.gradle.kts 解析器
type SettingsParser struct {
	Script GradleScriptParser
}

// SettingsResult settings.gradle.kts 解析结果
type SettingsResult struct {
	ProjectName        string
	Includes           []string
	Repositories       []string
	PluginRepositories []string
}

// Parse 解析 settings.gradle.kts
func (p SettingsParser) Parse(settingsPath string) SettingsResult {
	var result SettingsResult
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		return result
	}
	text := p.Script.StripComments(string(content))

	if m := rootProjectNameRe.FindStringSubmatch(text); m != nil {
		result.ProjectName = m[1]
	}

	seenInclude := map[string]bool{}
	for _, m := range includeParenRe.FindAllStringSubmatch(text, -1) {
		for _, inc := range p.Script.ExtractQuotedStrings(m[1]) {
			if !seenInclude[inc] {
				seenInclude[inc] = true
				result.Includes = append(result.Includes, inc)
			}
		}
	}
	for _, m := range includeNoParenRe.FindAllStringSubmatch(text, -1) {
		if !seenInclude[m[1]] {
			seenInclude[m[1]] = true
			result.Includes = append(result.Includes, m[1])
		}
	}

	if block, ok := p.Script.FindBlock(text, "pluginManagement"); ok {
		if reposBlock, ok2 := p.Script.FindBlock(block, "repositories"); ok2 {
			result.PluginRepositories = p.parseRepositories(reposBlock)
		}
	}
	if block, ok := p.Script.FindBlock(text, "dependencyResolutionManagement"); ok {
		if reposBlock, ok2 := p.Script.FindBlock(block, "repositories"); ok2 {
			result.Repositories = p.parseRepositories(reposBlock)
		}
	}
	return result
}

func (p SettingsParser) parseRepositories(block string) []string {
	var repos []string
	for _, stmt := range p.Script.GetTopLevelStatements(block) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		switch {
		case googleRepoRe.MatchString(stmt):
			repos = append(repos, "https://dl.google.com/dl/android/maven2")
		case mavenCentralRe.MatchString(stmt):
			repos = append(repos, "https://repo.maven.apache.org/maven2")
		case mavenLocalRe.MatchString(stmt):
			repos = append(repos, "mavenLocal")
		case pluginPortalRe.MatchString(stmt):
			repos = append(repos, "https://plugins.gradle.org/maven2")
		case strings.HasPrefix(stmt, "maven"):
			if m := uriRe.FindStringSubmatch(stmt); m != nil {
				repos = append(repos, m[1])
			} else if m := urlEqualsRe.FindStringSubmatch(stmt); m != nil {
				repos = append(repos, m[1])
			}
		}
	}
	return repos
}
