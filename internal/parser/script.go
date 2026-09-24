package parser

import (
	"regexp"
	"strconv"
	"strings"
)

// GradleScriptParser 花括号感知的 Kotlin DSL 脚本解析器（纯静态）
type GradleScriptParser struct{}

// FindBlock 查找指定块的内容（花括号匹配），返回块内部文本
func (GradleScriptParser) FindBlock(content, blockName string) (string, bool) {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(blockName) + `\s*\{`)
	match := re.FindStringIndex(content)
	if match == nil {
		return "", false
	}
	start := match[1]
	depth := 1
	inString := false
	var stringChar byte
	i := start
	for i < len(content) && depth > 0 {
		c := content[i]
		if inString {
			if c == '\\' {
				i += 2
				continue
			}
			if c == stringChar {
				inString = false
			}
		} else {
			if c == '"' || c == '\'' {
				inString = true
				stringChar = c
			} else if c == '/' && i+1 < len(content) && content[i+1] == '/' {
				// 行注释，跳到行尾
				for i < len(content) && content[i] != '\n' {
					i++
				}
				continue
			} else if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					return content[start:i], true
				}
			}
		}
		i++
	}
	return "", false
}

// FindBlocks 查找所有同名块
func (p GradleScriptParser) FindBlocks(content, blockName string) []string {
	var result []string
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(blockName) + `\s*\{`)
	searchStart := 0
	for searchStart < len(content) {
		match := re.FindStringIndex(content[searchStart:])
		if match == nil {
			break
		}
		absStart := searchStart + match[1]
		depth := 1
		inString := false
		var stringChar byte
		i := absStart
		found := false
		for i < len(content) && depth > 0 {
			c := content[i]
			if inString {
				if c == '\\' {
					i += 2
					continue
				}
				if c == stringChar {
					inString = false
				}
			} else {
				if c == '"' || c == '\'' {
					inString = true
					stringChar = c
				} else if c == '/' && i+1 < len(content) && content[i+1] == '/' {
					for i < len(content) && content[i] != '\n' {
						i++
					}
					continue
				} else if c == '{' {
					depth++
				} else if c == '}' {
					depth--
					if depth == 0 {
						result = append(result, content[absStart:i])
						found = true
						searchStart = i + 1
						break
					}
				}
			}
			i++
		}
		if !found {
			break
		}
	}
	return result
}

// GetTopLevelStatements 提取块内不进入嵌套块的顶层语句
func (GradleScriptParser) GetTopLevelStatements(blockContent string) []string {
	var statements []string
	var stmt strings.Builder
	depth := 0
	inString := false
	var stringChar byte
	flush := func() {
		if stmt.Len() > 0 {
			statements = append(statements, stmt.String())
			stmt.Reset()
		}
	}
	i := 0
	for i < len(blockContent) {
		c := blockContent[i]
		if inString {
			stmt.WriteByte(c)
			if c == '\\' {
				if i+1 < len(blockContent) {
					stmt.WriteByte(blockContent[i+1])
					i++
				}
			} else if c == stringChar {
				inString = false
			}
			i++
			continue
		}
		switch c {
		case '"', '\'':
			inString = true
			stringChar = c
			stmt.WriteByte(c)
		case '{':
			if depth == 0 && stmt.Len() > 0 {
				stmt.WriteString("{")
				statements = append(statements, stmt.String())
				stmt.Reset()
			}
			depth++
		case '}':
			depth--
			if depth == 0 {
				flush()
			}
		case '\n':
			if depth == 0 {
				flush()
			} else {
				stmt.WriteByte(c)
			}
		default:
			stmt.WriteByte(c)
		}
		i++
	}
	flush()
	return statements
}

// StripComments 去除 // 行注释和 /* */ 块注释
func (GradleScriptParser) StripComments(content string) string {
	var out strings.Builder
	inString := false
	var stringChar byte
	i := 0
	for i < len(content) {
		c := content[i]
		if inString {
			out.WriteByte(c)
			if c == '\\' {
				if i+1 < len(content) {
					out.WriteByte(content[i+1])
					i++
				}
			} else if c == stringChar {
				inString = false
			}
			i++
			continue
		}
		if c == '"' || c == '\'' {
			inString = true
			stringChar = c
			out.WriteByte(c)
			i++
			continue
		}
		if c == '/' && i+1 < len(content) {
			if content[i+1] == '/' {
				for i < len(content) && content[i] != '\n' {
					i++
				}
				continue
			}
			if content[i+1] == '*' {
				i += 2
				for i+1 < len(content) && !(content[i] == '*' && content[i+1] == '/') {
					i++
				}
				i += 2
				continue
			}
		}
		out.WriteByte(c)
		i++
	}
	return out.String()
}

// ExtractStringValue 提取 key = "value"
func (GradleScriptParser) ExtractStringValue(stmt, key string) (string, bool) {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]*)"`)
	m := re.FindStringSubmatch(stmt)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ExtractIntValue 提取 key = N
func (GradleScriptParser) ExtractIntValue(stmt, key string) (int, bool) {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\s*=\s*(\d+)`)
	m := re.FindStringSubmatch(stmt)
	if m == nil {
		return 0, false
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return v, true
}

// ExtractBoolValue 提取 key = true/false
func (GradleScriptParser) ExtractBoolValue(stmt, key string) (bool, bool) {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\s*=\s*(true|false)`)
	m := re.FindStringSubmatch(stmt)
	if m == nil {
		return false, false
	}
	return m[1] == "true", true
}

// ExtractQuotedStrings 抓取语句中所有引号字符串
func (GradleScriptParser) ExtractQuotedStrings(s string) []string {
	re := regexp.MustCompile(`"([^"]*)"`)
	matches := re.FindAllStringSubmatch(s, -1)
	var out []string
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}
