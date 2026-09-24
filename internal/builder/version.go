package builder

import (
	"strconv"
	"strings"
	"unicode"
)

// qualifierRank 版本限定符优先级（越小越旧），对齐 Maven 语义
var qualifierRank = map[string]int{
	"alpha":  0,
	"a":      0,
	"beta":   1,
	"b":      1,
	"milestone": 2,
	"m":      2,
	"rc":     3,
	"cr":     3,
	"snapshot": 4,
	"":       6,
	"ga":     6,
	"final":  6,
	"release": 6,
	"sp":     7,
}

type versionToken struct {
	isNumeric bool
	num       int
	str       string
}

// tokenizeVersion 把版本串拆成 token 序列（数字/限定符片段）
func tokenizeVersion(v string) []versionToken {
	var tokens []versionToken
	lower := strings.ToLower(v)
	i := 0
	for i < len(lower) {
		c := rune(lower[i])
		if unicode.IsDigit(c) {
			j := i
			for j < len(lower) && unicode.IsDigit(rune(lower[j])) {
				j++
			}
			n, _ := strconv.Atoi(lower[i:j])
			tokens = append(tokens, versionToken{isNumeric: true, num: n})
			i = j
			continue
		}
		if c == '.' || c == '-' {
			i++
			continue
		}
		j := i
		for j < len(lower) && !unicode.IsDigit(rune(lower[j])) && lower[j] != '.' && lower[j] != '-' {
			j++
		}
		if j > i {
			tokens = append(tokens, versionToken{str: lower[i:j]})
		}
		i = j
	}
	return tokens
}

func rankQualifier(q string) int {
	if r, ok := qualifierRank[q]; ok {
		return r
	}
	if q == "" {
		return 6
	}
	return 5 // 未知限定符介于 snapshot 与 ga 之间
}

// compareVersions 比较两个版本号。返回 -1 表示 a<b，0 相等，1 表示 a>b
func compareVersions(a, b string) int {
	ta := tokenizeVersion(a)
	tb := tokenizeVersion(b)

	n := len(ta)
	if len(tb) > n {
		n = len(tb)
	}
	for i := 0; i < n; i++ {
		// 取出当前位 token，越界视为"空数字 0"
		va := versionToken{}
		vb := versionToken{}
		hasA := i < len(ta)
		hasB := i < len(tb)
		if hasA {
			va = ta[i]
		}
		if hasB {
			vb = tb[i]
		}

		// 双方都是数字
		if va.isNumeric && vb.isNumeric {
			if va.num != vb.num {
				if va.num < vb.num {
					return -1
				}
				return 1
			}
			// 数字相等但一方继续有 token
			if hasA != hasB {
				return finishComparison(ta, tb, i+1, hasA, hasB)
			}
			continue
		}
		// 一侧耗尽：单方剩余数字段决定大小
		if hasA != hasB {
			return finishComparison(ta, tb, i, hasA, hasB)
		}
		// 数字 vs 限定符
		if va.isNumeric != vb.isNumeric {
			if va.isNumeric {
				return 1 // 数字段比同位的限定符新
			}
			return -1
		}
		// 限定符 vs 限定符
		ra := rankQualifier(va.str)
		rb := rankQualifier(vb.str)
		if ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
	}
	return 0
}

// finishComparison 在公共前缀耗尽后判断较长者大小：
// 较长方剩余全是"旧"限定符（alpha/beta/rc/snapshot）则更旧，否则更长=更大
func finishComparison(ta, tb []versionToken, start int, hasA, hasB bool) int {
	if hasA {
		if tailIsOldQualifier(ta, start) {
			return -1
		}
		return 1
	}
	if tailIsOldQualifier(tb, start) {
		return 1
	}
	return -1
}

func tailIsOldQualifier(tokens []versionToken, start int) bool {
	// Maven 语义：剩余 token 中若出现 pre-release 限定符（alpha/beta/rc/milestone/snapshot），
	// 无论后面是否带数字序号（如 alpha07），整体都是预发布、更旧。
	for i := start; i < len(tokens); i++ {
		if tokens[i].isNumeric {
			continue
		}
		if rankQualifier(tokens[i].str) <= 4 {
			return true
		}
		return false
	}
	return false
}

// selectHighestVersion 从候选版本集中选出最高版本；空集返回 ""
func selectHighestVersion(versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	best := versions[0]
	for _, v := range versions[1:] {
		if compareVersions(v, best) > 0 {
			best = v
		}
	}
	return best
}