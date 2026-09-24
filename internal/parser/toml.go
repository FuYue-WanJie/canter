package parser

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// VersionCatalogParser libs.versions.toml 解析器
type VersionCatalogParser struct{}

var (
	inlineDictQuotedRe = regexp.MustCompile(`(\w+(?:\.\w+)*)\s*=\s*"([^"]*)"`)
	inlineDictOtherRe  = regexp.MustCompile(`(\w+(?:\.\w+)*)\s*=\s*(true|false|\d+)`)
)

// Parse 解析 libs.versions.toml
func (VersionCatalogParser) Parse(tomlPath string) *VersionCatalog {
	catalog := NewVersionCatalog()
	file, err := os.Open(tomlPath)
	if err != nil {
		return catalog
	}
	defer file.Close()

	currentSection := ""
	content := ""
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	content = strings.Join(lines, "\n")
	_ = content

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eqIdx := strings.Index(line, "=")
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eqIdx])
		value := strings.TrimSpace(line[eqIdx+1:])

		switch currentSection {
		case "versions":
			catalog.Versions[key] = strings.Trim(value, "\"")
		case "libraries":
			if strings.HasPrefix(value, "{") {
				lib := parseInlineDict(content, rawLine, value)
				catalog.Libraries[key] = lib
			} else {
				coord := strings.Trim(value, "\"")
				parts := strings.Split(coord, ":")
				if len(parts) >= 2 {
					lib := map[string]string{"group": parts[0], "name": parts[1]}
					if len(parts) >= 3 {
						lib["version"] = parts[2]
					}
					catalog.Libraries[key] = lib
				}
			}
		case "plugins":
			if strings.HasPrefix(value, "{") {
				plugin := parseInlineDict(content, rawLine, value)
				catalog.Plugins[key] = plugin
			} else {
				pid := strings.Trim(value, "\"")
				catalog.Plugins[key] = map[string]string{"id": pid}
			}
		case "bundles":
			items := grepQuoted(value)
			catalog.Bundles[key] = items
		}
	}
	return catalog
}

func grepQuoted(s string) []string {
	re := regexp.MustCompile(`"([^"]*)"`)
	matches := re.FindAllStringSubmatch(s, -1)
	var out []string
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

func parseInlineDict(content, line, value string) map[string]string {
	full := value
	if !strings.HasSuffix(value, "}") {
		// 从原 content 中该行之后继续拼接
		idx := strings.Index(content, line)
		if idx >= 0 {
			rest := content[idx+len(line):]
			end := strings.Index(rest, "}")
			if end >= 0 {
				full = value + " " + rest[:end] + "}"
			}
		}
	}
	dict := map[string]string{}
	for _, m := range inlineDictQuotedRe.FindAllStringSubmatch(full, -1) {
		dict[m[1]] = m[2]
	}
	for _, m := range inlineDictOtherRe.FindAllStringSubmatch(full, -1) {
		if _, exists := dict[m[1]]; !exists {
			dict[m[1]] = m[2]
		}
	}
	return dict
}

// GradlePropertiesParser gradle.properties 解析器
type GradlePropertiesParser struct{}

// Parse 解析 gradle.properties
func (GradlePropertiesParser) Parse(propsPath string) map[string]string {
	result := map[string]string{}
	file, err := os.Open(propsPath)
	if err != nil {
		return result
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eqIdx := strings.Index(line, "=")
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eqIdx])
		val := strings.TrimSpace(line[eqIdx+1:])
		result[key] = val
	}
	return result
}
