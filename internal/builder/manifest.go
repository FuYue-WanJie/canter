package builder

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"canter/internal/xmldom"
)

// ManifestMessage manifest 合并过程中的消息
type ManifestMessage struct {
	Severity string // INFO / WARNING / ERROR
	Text     string
}

// ManifestMergeResult 合并结果
type ManifestMergeResult struct {
	XML      string
	Messages []ManifestMessage
	Errors   []string
}

// manifestMerger 仿 CodeAssist ManifestMerger2 的合并器
type manifestMerger struct {
	placeholders map[string]string
	messages     []ManifestMessage
}

var placeholderRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// manifestNodeKey 节点身份 key（元素类型 + 标识属性）
func manifestNodeKey(e *xmldom.Element) string {
	typeName := e.LocalName
	switch typeName {
	case "manifest", "application", "uses-sdk", "supports-screens", "compatible-screens", "supports-gl-texture":
		return typeName
	case "intent-filter", "intent":
		return typeName + "|" + intentFilterKey(e)
	case "data":
		return typeName + "|" + dataKey(e)
	case "uses-feature":
		if v, ok := androidAttr(e, "name"); ok {
			return typeName + "|" + v
		}
		if v, ok := androidAttr(e, "glEsVersion"); ok {
			return typeName + "|glEs:" + v
		}
		return typeName + "|"
	case "provider":
		if v, ok := androidAttr(e, "name"); ok {
			return typeName + "|" + v
		}
		if v, ok := androidAttr(e, "authorities"); ok {
			return typeName + "|auth:" + v
		}
		return typeName + "|"
	case "screen":
		if v, ok := androidAttr(e, "screenSize"); ok {
			return typeName + "|" + v
		}
		return typeName + "|"
	default:
		if v, ok := androidAttr(e, "name"); ok {
			return typeName + "|" + v
		}
		return typeName + "|" + signatureOf(e)
	}
}

func androidAttr(e *xmldom.Element, local string) (string, bool) {
	return e.GetAttrNS(xmldom.AndroidNS, local)
}

func intentFilterKey(e *xmldom.Element) string {
	var actions, cats, datas []string
	for _, c := range e.Children {
		if c.LocalName == "action" {
			if v, ok := androidAttr(c, "name"); ok {
				actions = append(actions, v)
			}
		} else if c.LocalName == "category" {
			if v, ok := androidAttr(c, "name"); ok {
				cats = append(cats, v)
			}
		} else if c.LocalName == "data" {
			datas = append(datas, dataKey(c))
		}
	}
	sort.Strings(actions)
	sort.Strings(cats)
	sort.Strings(datas)
	return fmt.Sprintf("a=%s;c=%s;d=%s", strings.Join(actions, ","), strings.Join(cats, ","), strings.Join(datas, ","))
}

func dataKey(e *xmldom.Element) string {
	var parts []string
	for _, a := range e.Attrs {
		if a.NS == xmldom.AndroidNS {
			parts = append(parts, a.Local+"="+a.Value)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func signatureOf(e *xmldom.Element) string {
	var parts []string
	for _, a := range e.Attrs {
		if a.NS != xmldom.ToolsNS && !isXMLNSDecl(a.Name) {
			parts = append(parts, a.Name+"="+a.Value)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func isXMLNSDecl(name string) bool {
	return name == "xmlns" || strings.HasPrefix(name, "xmlns:")
}

func toolsAttr(e *xmldom.Element, local string) (string, bool) {
	return e.GetAttrNS(xmldom.ToolsNS, local)
}

func toolsNode(e *xmldom.Element) string {
	if v, ok := toolsAttr(e, "node"); ok {
		return strings.ToLower(v)
	}
	return ""
}

func toolsList(e *xmldom.Element, local string) map[string]bool {
	v, ok := toolsAttr(e, local)
	if !ok {
		return map[string]bool{}
	}
	result := map[string]bool{}
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if idx := strings.Index(p, ":"); idx >= 0 {
			p = p[idx+1:]
		}
		if p != "" {
			result[p] = true
		}
	}
	return result
}

func getAttr(e *xmldom.Element, ns, local string) (string, bool) {
	if ns != "" {
		return e.GetAttrNS(ns, local)
	}
	return e.GetAttrLocal(local)
}

// isPrimaryAuthoritative 应用清单独占的属性（包名、uses-sdk 版本），库的差异被静默吸收
func isPrimaryAuthoritative(into *xmldom.Element, ns, local string) bool {
	switch into.LocalName {
	case "manifest":
		return ns == "" && local == "package"
	case "uses-sdk":
		return ns == xmldom.AndroidNS && (local == "minSdkVersion" || local == "targetSdkVersion" || local == "maxSdkVersion")
	}
	return false
}

// mergeManifest 合并 manifest：primary 为应用清单（最高优先级），libraries 按降序排列。
// appPackage 为应用的命名空间（Kotlin DSL 项目 manifest 无 package 属性时注入）。
func mergeManifest(primaryPath string, libraryPaths []string, placeholders map[string]string, appMinSdk int, stripVersionCode, stripVersionName bool) ManifestMergeResult {
	return mergeManifestWithPackage(primaryPath, libraryPaths, placeholders, appMinSdk, stripVersionCode, stripVersionName, "")
}

func mergeManifestWithPackage(primaryPath string, libraryPaths []string, placeholders map[string]string, appMinSdk int, stripVersionCode, stripVersionName bool, appPackage string) ManifestMergeResult {
	m := &manifestMerger{placeholders: placeholders}

	primaryData, err := os.ReadFile(primaryPath)
	var primaryDoc *xmldom.Doc
	var result ManifestMergeResult
	if err != nil {
		result.XML = ""
		result.Errors = append(result.Errors, fmt.Sprintf("读取主 manifest 失败: %v", err))
		return result
	}
	primaryDoc, err = xmldom.Parse(primaryData)
	if err != nil {
		result.XML = string(primaryData)
		result.Errors = append(result.Errors, "primary manifest is not valid XML: "+err.Error())
		return result
	}
	primaryRoot := primaryDoc.Root
	// Kotlin DSL 项目清单无 package 属性，从 namespace 注入（对齐 AGP）
	if appPackage != "" {
		if _, ok := primaryRoot.GetAttrLocal("package"); !ok {
			primaryRoot.SetAttrNS("", "package", appPackage)
		}
	}
	m.substitutePlaceholders(primaryRoot, "primary manifest")

	// 应用的 minSdk：build config 优先，其次应用 manifest，最后 API 1
	effectiveAppMin := appMinSdk
	overrideLibs := map[string]bool{}
	if appUsesSDK := firstChild(primaryRoot, "uses-sdk"); appUsesSDK != nil {
		if effectiveAppMin == 0 {
			if v, ok := androidAttr(appUsesSDK, "minSdkVersion"); ok {
				n := 0
				fmt.Sscanf(v, "%d", &n)
				effectiveAppMin = n
			}
		}
		for lib := range toolsList(appUsesSDK, "overrideLibrary") {
			overrideLibs[lib] = true
		}
	}
	if effectiveAppMin == 0 {
		effectiveAppMin = 1
	}

	for _, libPath := range libraryPaths {
		data, err := os.ReadFile(libPath)
		if err != nil {
			m.messages = append(m.messages, ManifestMessage{"WARNING", fmt.Sprintf("跳过 %s: %v", libPath, err)})
			continue
		}
		libDoc, err := xmldom.Parse(data)
		if err != nil {
			m.messages = append(m.messages, ManifestMessage{"WARNING", fmt.Sprintf("跳过 %s: not valid XML", libPath)})
			continue
		}
		libRoot := libDoc.Root
		m.substitutePlaceholders(libRoot, filepath.Base(libPath))
		libPkg, hasPkg := libRoot.GetAttrLocal("package")
		libPkgName := ""
		if hasPkg && libPkg != "" {
			libPkgName = libPkg
		}
		// minSdk 下限检查
		m.checkLibraryMinSdk(libRoot, filepath.Base(libPath), libPkgName, effectiveAppMin, overrideLibs)
		// 展开相对类名
		if libPkgName != "" {
			m.expandClassNames(libRoot, libPkgName)
		}
		m.mergeElement(primaryRoot, libRoot, filepath.Base(libPath), libPkgName)
	}

	m.applyRemovals(primaryRoot)
	m.stripToolsArtifacts(primaryRoot)
	m.validateIdentifierAttributes(primaryRoot)

	if stripVersionCode {
		primaryRoot.RemoveAttrNS(xmldom.AndroidNS, "versionCode")
	}
	if stripVersionName {
		primaryRoot.RemoveAttrNS(xmldom.AndroidNS, "versionName")
	}

	result.XML = primaryDoc.Marshal()
	result.Messages = m.messages
	for _, msg := range m.messages {
		if msg.Severity == "ERROR" {
			result.Errors = append(result.Errors, msg.Text)
		}
	}
	return result
}

func firstChild(e *xmldom.Element, name string) *xmldom.Element {
	for _, c := range e.Children {
		if c.LocalName == name {
			return c
		}
	}
	return nil
}

func (m *manifestMerger) mergeElement(into, from *xmldom.Element, lib, libPkg string) {
	switch toolsNode(into) {
	case "replace", "remove", "removeAll":
		return
	}
	m.mergeAttributes(into, from, lib)
	if toolsNode(into) == "mergeOnlyAttributes" {
		return
	}
	m.mergeChildren(into, from, lib, libPkg)
}

func (m *manifestMerger) mergeAttributes(into, from *xmldom.Element, lib string) {
	replace := toolsList(into, "replace")
	strict := toolsNode(into) == "strict"
	for _, a := range from.Attrs {
		if a.NS == xmldom.ToolsNS {
			continue
		}
		if isXMLNSDecl(a.Name) {
			continue
		}
		if isPrimaryAuthoritative(into, a.NS, a.Local) {
			continue
		}
		higher, ok := getAttr(into, a.NS, a.Local)
		switch {
		case !ok:
			into.Attrs = append(into.Attrs, copyAttr(a))
		case higher == a.Value:
		case replace[a.Local]:
		case strict:
			m.messages = append(m.messages, ManifestMessage{"ERROR",
				fmt.Sprintf("manifest conflict on @%s (\"%s\" vs \"%s\" from %s); tools:strict", a.Name, higher, a.Value, lib)})
		default:
			m.messages = append(m.messages, ManifestMessage{"WARNING",
				fmt.Sprintf("manifest attribute @%s differs (kept \"%s\", %s has \"%s\"); add tools:replace=\"%s\" to silence",
					a.Name, higher, lib, a.Value, a.Name)})
		}
	}
}

func copyAttr(a xmldom.Attr) xmldom.Attr {
	return a
}

func (m *manifestMerger) mergeChildren(into, from *xmldom.Element, lib, libPkg string) {
	existing := map[string]*xmldom.Element{}
	for _, c := range into.Children {
		existing[manifestNodeKey(c)] = c
	}
	removeAllTypes := map[string]string{}
	for _, c := range into.Children {
		if toolsNode(c) == "removeAll" {
			sel, _ := toolsAttr(c, "selector")
			removeAllTypes[c.LocalName] = sel
		}
	}
	for _, child := range from.Children {
		typeName := child.LocalName
		if selector, blocked := removeAllTypes[typeName]; blocked {
			if selector == "" || selector == libPkg {
				continue
			}
		}
		key := manifestNodeKey(child)
		match, exists := existing[key]
		if !exists {
			if typeName == "uses-sdk" {
				continue
			}
			into.Children = append(into.Children, child.Clone())
		} else {
			switch toolsNode(match) {
			case "remove", "removeAll", "replace":
			default:
				m.mergeElement(match, child, lib, libPkg)
			}
		}
	}
}

func (m *manifestMerger) checkLibraryMinSdk(libRoot *xmldom.Element, lib, libPkg string, appMin int, overrideLibs map[string]bool) {
	libUsesSDK := firstChild(libRoot, "uses-sdk")
	if libUsesSDK == nil {
		return
	}
	v, ok := androidAttr(libUsesSDK, "minSdkVersion")
	if !ok {
		return
	}
	libMin := 0
	fmt.Sscanf(v, "%d", &libMin)
	if libMin == 0 {
		return
	}
	if libMin <= appMin || (libPkg != "" && overrideLibs[libPkg]) {
		return
	}
	fix := ""
	if libPkg != "" {
		fix = fmt.Sprintf(" Raise the app's minSdk to at least %d, or add tools:overrideLibrary=\"%s\" to force it.", libMin, libPkg)
	} else {
		fix = fmt.Sprintf(" Raise the app's minSdk to at least %d.", libMin)
	}
	// 对齐 Gradle：该检查为 warning 而非致命错误（app 的 minSdk 保持权威），避免比 Gradle 更严格地阻断构建
	m.messages = append(m.messages, ManifestMessage{"WARNING",
		fmt.Sprintf("uses-sdk:minSdkVersion %d smaller than library %s's %d.%s", appMin, lib, libMin, fix)})
}

// 组件类名属性映射
var classNameAttrs = map[string][]string{
	"application":      {"name", "backupAgent"},
	"activity":         {"name", "parentActivityName"},
	"activity-alias":   {"name", "targetActivity"},
	"service":          {"name"},
	"receiver":         {"name"},
	"provider":         {"name"},
	"instrumentation":  {"name"},
}

func (m *manifestMerger) expandClassNames(el *xmldom.Element, pkg string) {
	if locals, ok := classNameAttrs[el.LocalName]; ok {
		for _, local := range locals {
			v, has := el.GetAttrNS(xmldom.AndroidNS, local)
			if !has {
				continue
			}
			resolved := resolveClassName(v, pkg)
			if resolved != v {
				el.SetAttrNS(xmldom.AndroidNS, local, resolved)
			}
		}
	}
	for _, c := range el.Children {
		m.expandClassNames(c, pkg)
	}
}

func resolveClassName(name, pkg string) string {
	switch {
	case name == "":
		return name
	case name[0] == '.':
		return pkg + name
	case !strings.Contains(name, "."):
		return pkg + "." + name
	default:
		return name
	}
}

func (m *manifestMerger) substitutePlaceholders(root *xmldom.Element, where string) {
	for i := range root.Attrs {
		a := &root.Attrs[i]
		if !strings.Contains(a.Value, "${") {
			continue
		}
		a.Value = placeholderRe.ReplaceAllStringFunc(a.Value, func(matched string) string {
			mm := placeholderRe.FindStringSubmatch(matched)
			if len(mm) < 2 {
				return matched
			}
			key := mm[1]
			if val, ok := m.placeholders[key]; ok {
				return val
			}
			m.messages = append(m.messages, ManifestMessage{"WARNING",
				fmt.Sprintf("unresolved manifest placeholder ${%s} in @%s (%s)", key, a.Name, where)})
			return matched
		})
	}
	for _, c := range root.Children {
		m.substitutePlaceholders(c, where)
	}
}

func (m *manifestMerger) applyRemovals(root *xmldom.Element) {
	var toRemove []*xmldom.Element
	for _, c := range root.Children {
		mode := toolsNode(c)
		if mode == "remove" || mode == "removeAll" {
			toRemove = append(toRemove, c)
			continue
		}
		m.applyRemovals(c)
	}
	for _, c := range toRemove {
		root.RemoveChild(c)
	}
}

func (m *manifestMerger) stripToolsArtifacts(root *xmldom.Element) {
	for local := range toolsList(root, "remove") {
		root.RemoveAttrNS(xmldom.AndroidNS, local)
		root.RemoveAttrNS("", local)
	}
	var toolsAttrs []int
	for i, a := range root.Attrs {
		if a.NS == xmldom.ToolsNS {
			toolsAttrs = append(toolsAttrs, i)
		}
	}
	if len(toolsAttrs) > 0 {
		out := root.Attrs[:0]
		skip := map[int]bool{}
		for _, idx := range toolsAttrs {
			skip[idx] = true
		}
		for i, a := range root.Attrs {
			if skip[i] {
				continue
			}
			out = append(out, a)
		}
		root.Attrs = out
	}
	for _, c := range root.Children {
		m.stripToolsArtifacts(c)
	}
}

// 标识符属性验证：空的或未解析的 placeholder 是硬错误
func (m *manifestMerger) validateIdentifierAttributes(root *xmldom.Element) {
	var checkName func(e *xmldom.Element)
	checkName = func(e *xmldom.Element) {
		if v, ok := e.GetAttrNS(xmldom.AndroidNS, "name"); ok && (v == "" || strings.Contains(v, "${")) {
			m.reportIdentifierError(e, "android:name", v)
		}
	}
	switch root.LocalName {
	case "manifest":
		if v, ok := root.GetAttrLocal("package"); ok && strings.Contains(v, "${") {
			m.reportIdentifierError(root, "package", v)
		}
	case "activity", "activity-alias", "service", "receiver", "provider", "application",
		"instrumentation", "permission", "permission-group", "permission-tree", "uses-permission", "package":
		checkName(root)
	}
	if root.LocalName == "provider" {
		if v, ok := root.GetAttrNS(xmldom.AndroidNS, "authorities"); ok && (v == "" || strings.Contains(v, "${")) {
			m.reportIdentifierError(root, "android:authorities", v)
		}
	}
	for _, c := range root.Children {
		m.validateIdentifierAttributes(c)
	}
}

func (m *manifestMerger) reportIdentifierError(e *xmldom.Element, attrLabel, value string) {
	reason := "is empty"
	if strings.Contains(value, "${") {
		reason = "has an unresolved ${} placeholder"
	}
	m.messages = append(m.messages, ManifestMessage{"ERROR",
		fmt.Sprintf("<%s> %s %s (\"%s\"): aapt2 requires a valid identifier here.", e.LocalName, attrLabel, reason, value)})
}