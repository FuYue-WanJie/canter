package builder

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"canter/internal/xmldom"
)

// 内置 AGP 默认 proguard 规则（vendor 自 AGP 9.x，9.2.1 与 9.3.3 内容一致），
// 对应 getDefaultProguardFile("proguard-android.txt" / "proguard-android-optimize.txt")。
//
//go:embed pgconf/proguard-common.txt
var proguardCommon string

//go:embed pgconf/proguard-optimizations.txt
var proguardOptimizations string

// defaultProguardRuleFile 返回指定默认规则文件的内容（对齐 AGP 的拼接方式）
func defaultProguardRuleFile(name string) (string, bool) {
	switch name {
	case "proguard-android.txt":
		return proguardCommon, true
	case "proguard-android-optimize.txt":
		return proguardCommon + "\n" + proguardOptimizations + "\n", true
	}
	return "", false
}

// isDefaultProguardFile 判断 proguardFiles 中的条目是否为默认规则文件名
// （parser 从 getDefaultProguardFile("...") 抓到的是裸文件名）
func isDefaultProguardFile(s string) bool {
	_, ok := defaultProguardRuleFile(s)
	return ok
}

// writeDefaultProguardFile 把内置默认规则写到构建目录，返回文件路径
func writeDefaultProguardFile(buildDir, name string) (string, error) {
	content, ok := defaultProguardRuleFile(name)
	if !ok {
		return "", fmt.Errorf("未知默认 proguard 文件: %s", name)
	}
	out := filepath.Join(buildDir, "pgconf", name)
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(out, []byte(content), 0644); err != nil {
		return "", err
	}
	return out, nil
}

// manifestComponentKeepRules 从合并后的 manifest 提取四大组件与
// Application 的 keep 规则。R8 靠 manifest 反射实例化这些类，收缩前必须保留。
func manifestComponentKeepRules(manifestPath string) (string, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", err
	}
	doc, err := xmldom.Parse(data)
	if err != nil {
		return "", err
	}
	app := doc.Root
	if app == nil {
		return "", fmt.Errorf("manifest 无根元素")
	}

	// 定位 <application> 元素（组件都在其下）
	var appEl *xmldom.Element
	for _, el := range app.ChildrenList() {
		if el.LocalName == "application" {
			appEl = el
			break
		}
	}

	keepClasses := map[string]bool{}
	// 相对类名（以 "." 开头）相对 manifest package 展开
	pkg := ""
	if p, ok := app.GetAttrLocal("package"); ok {
		pkg = p
	}
	if appEl != nil {
		// application 自身的 android:name
		if name, ok := appEl.GetAttrNS(xmldom.AndroidNS, "name"); ok && name != "" {
			keepClasses[name] = true
		}
		for _, tag := range []string{"activity", "activity-alias", "service", "receiver", "provider"} {
			for _, el := range appEl.ChildrenList() {
				if el.LocalName != tag {
					continue
				}
				if name, ok := el.GetAttrNS(xmldom.AndroidNS, "name"); ok && name != "" {
					keepClasses[name] = true
				}
			}
		}
	}
	expanded := map[string]bool{}
	for name := range keepClasses {
		cls := name
		if strings.HasPrefix(cls, ".") {
			cls = pkg + cls
		} else if !strings.Contains(cls, ".") && pkg != "" {
			cls = pkg + "." + cls
		}
		expanded[cls] = true
	}
	keepClasses = expanded

	var sb strings.Builder
	sb.WriteString("# 由 manifest 组件生成（对齐 AGP 的 manifest keep 规则）\n")
	for _, cls := range sortedKeys(keepClasses) {
		fmt.Fprintf(&sb, "-keep class %s { *; }\n", cls)
	}
	return sb.String(), nil
}

// writeComponentKeepRules 把组件 keep 规则写入构建目录并返回路径（无组件时返回空）
func writeComponentKeepRules(buildDir, manifestPath string) (string, error) {
	rules, err := manifestComponentKeepRules(manifestPath)
	if err != nil {
		return "", err
	}
	if !strings.Contains(rules, "-keep") {
		return "", nil
	}
	out := filepath.Join(buildDir, "pgconf", "manifest-keep.pro")
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(out, []byte(rules), 0644); err != nil {
		return "", err
	}
	return out, nil
}

// sortedKeys 返回 map 键的有序列表
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
