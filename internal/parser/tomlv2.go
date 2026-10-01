package parser

import (
	"strconv"
	"strings"
)

// tomlDoc 是极简 TOML 解析结果：root 表 + 嵌套表。
// 支持 [a.b]、[[a]]、key = "str" / 数字 / bool / [array] / { inline = dict }。
type tomlDoc struct {
	root *tomlTable
}

// tomlTable 表示一个 TOML 表（可含子表与数组表）。
type tomlTable struct {
	values  map[string]string
	subs    map[string]*tomlTable   // [a.b] -> b 作为 a 的子表
	arrays  map[string][]*tomlTable // [[a]] -> 每个元素
	arrVals map[string][]string     // key = ["x","y"] 字符串数组
}

func newTOMLTable() *tomlTable {
	return &tomlTable{
		values:  map[string]string{},
		subs:    map[string]*tomlTable{},
		arrays:  map[string][]*tomlTable{},
		arrVals: map[string][]string{},
	}
}

// parseTOML 解析 TOML 文本。
func parseTOML(text string) *tomlDoc {
	doc := &tomlDoc{root: newTOMLTable()}
	root := doc.root
	current := root
	// arrayTableTrackers 记录当前 [[name]] 正在追加的表，键为完整路径。
	arrayCurrent := map[string]*tomlTable{}

	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(stripTOMLComment(lines[i]))
		if line == "" {
			continue
		}
		// 跨行数组/内联字典拼接
		line, i = joinContinuation(line, lines, i)

		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			path := strings.TrimSpace(line[2 : len(line)-2])
			tbl := newTOMLTable()
			parent, key := navigateForArray(root, splitTOMLPath(path))
			parent.arrays[key] = append(parent.arrays[key], tbl)
			arrayCurrent[path] = tbl
			current = tbl
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			path := strings.TrimSpace(line[1 : len(line)-1])
			tbl := navigate(root, splitTOMLPath(path))
			current = tbl
			continue
		}
		eq := indexTopLevelEq(line)
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		storeTOMLValue(current, trimTOMLKey(key), val)
	}
	return doc
}

// joinContinuation 若一行以未闭合的 [ 或 { 结尾，则与后续行拼接。
func joinContinuation(line string, lines []string, idx int) (string, int) {
	for !tomlValueBalanced(line) && idx+1 < len(lines) {
		idx++
		next := strings.TrimSpace(stripTOMLComment(lines[idx]))
		line += " " + next
	}
	return line, idx
}

func tomlValueBalanced(s string) bool {
	var bracket, brace int
	inStr := false
	var strCh byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' {
				i++
			} else if c == strCh {
				inStr = false
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = true
			strCh = c
		case '[':
			bracket++
		case ']':
			bracket--
		case '{':
			brace++
		case '}':
			brace--
		}
	}
	return bracket <= 0 && brace <= 0
}

func storeTOMLValue(t *tomlTable, key, val string) {
	if strings.HasPrefix(val, "{") {
		// 内联字典：展开为子表
		sub := newTOMLTable()
		for k, v := range parseInlineTOMLDict(val) {
			storeTOMLValue(sub, k, v)
		}
		t.subs[key] = sub
		return
	}
	if strings.HasPrefix(val, "[") {
		t.arrVals[key] = parseTOMLStringArray(val)
		return
	}
	t.values[key] = unquoteTOML(val)
}

// parseInlineTOMLDict 解析 { a = "x", b = 1 } 为 key->原始值串。
func parseInlineTOMLDict(s string) map[string]string {
	inner := strings.TrimSpace(s)
	inner = strings.TrimPrefix(inner, "{")
	inner = strings.TrimSuffix(inner, "}")
	out := map[string]string{}
	for _, part := range splitTopLevelCommas(inner) {
		eq := indexTopLevelEq(part)
		if eq < 0 {
			continue
		}
		k := trimTOMLKey(strings.TrimSpace(part[:eq]))
		v := strings.TrimSpace(part[eq+1:])
		out[k] = v
	}
	return out
}

// parseTOMLStringArray 解析 ["a", "b"]。
func parseTOMLStringArray(s string) []string {
	inner := strings.TrimSpace(s)
	inner = strings.TrimPrefix(inner, "[")
	inner = strings.TrimSuffix(inner, "]")
	if strings.TrimSpace(inner) == "" {
		return nil
	}
	var out []string
	for _, part := range splitTopLevelCommas(inner) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, unquoteTOML(part))
	}
	return out
}

// splitTopLevelCommas 按顶层逗号分割（忽略引号与嵌套括号内的逗号）。
func splitTopLevelCommas(s string) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	inStr := false
	var strCh byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			cur.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
			} else if c == strCh {
				inStr = false
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = true
			strCh = c
			cur.WriteByte(c)
		case '[', '{':
			depth++
			cur.WriteByte(c)
		case ']', '}':
			depth--
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		parts = append(parts, cur.String())
	}
	return parts
}

// indexTopLevelEq 返回顶层 '=' 的下标（忽略引号 / 嵌套 / version.ref 中的点）。
func indexTopLevelEq(s string) int {
	inStr := false
	var strCh byte
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' {
				i++
			} else if c == strCh {
				inStr = false
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = true
			strCh = c
		case '[', '{', '(':
			depth++
		case ']', '}', ')':
			depth--
		case '=':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func stripTOMLComment(line string) string {
	inStr := false
	var strCh byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inStr {
			if c == '\\' {
				i++
			} else if c == strCh {
				inStr = false
			}
			continue
		}
		if c == '"' || c == '\'' {
			inStr = true
			strCh = c
			continue
		}
		if c == '#' {
			return line[:i]
		}
	}
	return line
}

func trimTOMLKey(k string) string { return unquoteTOML(strings.TrimSpace(k)) }

func unquoteTOML(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		inner := s[1 : len(s)-1]
		if s[0] == '"' {
			if u, err := strconv.Unquote(s); err == nil {
				return u
			}
		}
		return inner
	}
	return s
}

func splitTOMLPath(path string) []string {
	var out []string
	var cur strings.Builder
	inStr := false
	var strCh byte
	for i := 0; i < len(path); i++ {
		c := path[i]
		if inStr {
			cur.WriteByte(c)
			if c == '\\' && i+1 < len(path) {
				cur.WriteByte(path[i+1])
				i++
			} else if c == strCh {
				inStr = false
			}
			continue
		}
		if c == '"' || c == '\'' {
			inStr = true
			strCh = c
			cur.WriteByte(c)
			continue
		}
		if c == '.' {
			out = append(out, trimTOMLKey(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		out = append(out, trimTOMLKey(cur.String()))
	}
	return out
}

// navigate 沿路径创建/定位子表（用于 [a.b.c]）。
// 若某段命中已存在的数组表（[[a]]），则进入其最后一个元素，
// 以符合 TOML 中 [a.b] 紧跟在 [[a]] 之后表示“最后一个 a 元素的子表”的语义。
func navigate(root *tomlTable, path []string) *tomlTable {
	cur := root
	for _, part := range path {
		if arr := cur.arrays[part]; len(arr) > 0 {
			cur = arr[len(arr)-1]
			continue
		}
		if next, ok := cur.subs[part]; ok {
			cur = next
			continue
		}
		next := newTOMLTable()
		cur.subs[part] = next
		cur = next
	}
	return cur
}

// navigateForArray 定位数组表的父表与 key。
func navigateForArray(root *tomlTable, path []string) (*tomlTable, string) {
	if len(path) == 1 {
		return root, path[0]
	}
	parent := navigate(root, path[:len(path)-1])
	return parent, path[len(path)-1]
}

// ---- 取值辅助 ----

func (t *tomlTable) getString(keys ...string) string {
	tbl := t
	for _, k := range keys[:len(keys)-1] {
		tbl = tbl.subs[k]
		if tbl == nil {
			return ""
		}
	}
	if tbl == nil {
		return ""
	}
	return tbl.values[keys[len(keys)-1]]
}

func (t *tomlTable) getStringSlice(key string) []string {
	if t == nil {
		return nil
	}
	return t.arrVals[key]
}

func (t *tomlTable) getBool(key string) bool {
	if t == nil {
		return false
	}
	return t.values[key] == "true"
}

func (t *tomlTable) getBoolPtr(key string) (bool, bool) {
	if t == nil {
		return false, false
	}
	v, ok := t.values[key]
	if !ok {
		return false, false
	}
	return v == "true", true
}

func (t *tomlTable) getInt(key string) (int, bool) {
	if t == nil {
		return 0, false
	}
	v, ok := t.values[key]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (t *tomlTable) getFloat(key string) (float64, bool) {
	if t == nil {
		return 0, false
	}
	v, ok := t.values[key]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func (t *tomlTable) getTable(key string) *tomlTable {
	if t == nil {
		return nil
	}
	return t.subs[key]
}

// tableFlat 返回子表的所有标量值（用于 [properties] / [versions]）。
func tableFlat(t *tomlTable, key string) map[string]string {
	sub := t.getTable(key)
	if sub == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range sub.values {
		out[k] = v
	}
	return out
}

// subtables 返回某 key 下的所有命名子表（用于 [libraries] 的内联元素）。
func subtables(t *tomlTable, key string) map[string]*tomlTable {
	sub := t.getTable(key)
	if sub == nil {
		return nil
	}
	return sub.subs
}

// arrayTables 返回 [[key]] 数组表的元素。
func arrayTables(t *tomlTable, key string) []*tomlTable {
	if t == nil {
		return nil
	}
	return t.arrays[key]
}
