package xmldom

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Attr XML 属性
type Attr struct {
	Name  string // 完整名（含前缀，如 android:name）
	NS    string // 命名空间 URI
	Local string // 本地名（不含前缀）
	Value string
}

// Element XML 元素（DOM 树）
type Element struct {
	Name       string //  tagName（含前缀，如 intent-filter）
	LocalName  string
	Attrs      []Attr
	Children   []*Element
	Parent     *Element
}

// Doc 文档根
type Doc struct {
	Root *Element
}

// Namespace 常量
const (
	AndroidNS = "http://schemas.android.com/apk/res/android"
	ToolsNS   = "http://schemas.android.com/tools"
)

// xmlnsNS 声明 xmlns:xxx 时 Go 给的名字空间占位
const xmlnsNS = "xmlns"

// qualifiedName 根据命名空间 URI 重建带前缀的属性名
func qualifiedName(ns, local string) string {
	switch ns {
	case AndroidNS:
		return "android:" + local
	case ToolsNS:
		return "tools:" + local
	case xmlnsNS:
		return "xmlns:" + local
	default:
		return local
	}
}

// Parse 解析 XML 为 DOM 树
func Parse(data []byte) (*Doc, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true

	var root *Element
	var stack []*Element
	var parent *Element

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			el := &Element{
				Name:      t.Name.Local,
				LocalName: t.Name.Local,
				Attrs:     make([]Attr, 0, len(t.Attr)),
			}
			for _, a := range t.Attr {
				attr := Attr{
					Name:  qualifiedName(a.Name.Space, a.Name.Local),
					NS:    a.Name.Space,
					Local: a.Name.Local,
					Value: a.Value,
				}
				el.Attrs = append(el.Attrs, attr)
			}
			if parent != nil {
				el.Parent = parent
				parent.Children = append(parent.Children, el)
			} else {
				root = el
			}
			stack = append(stack, el)
			parent = el
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			} else {
				parent = nil
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("no root element")
	}
	return &Doc{Root: root}, nil
}

// ParseString 解析 XML 字符串
func ParseString(s string) (*Doc, error) {
	return Parse([]byte(s))
}

// Children 子元素列表（非叶子即元素）
func (e *Element) ChildrenList() []*Element {
	return e.Children
}

// HasAttr 检查指定命名空间/本地名属性是否存在
func (e *Element) HasAttrNS(ns, local string) bool {
	_, ok := e.GetAttrNS(ns, local)
	return ok
}

// GetAttrNS 获取指定命名空间/本地名属性值
func (e *Element) GetAttrNS(ns, local string) (string, bool) {
	for _, a := range e.Attrs {
		if a.Local == local {
			if ns == "" {
				return a.Value, true
			}
			if a.NS == ns {
				return a.Value, true
			}
		}
	}
	return "", false
}

// GetAttrLocal 获取本地名属性（不区分命名空间前缀）
func (e *Element) GetAttrLocal(local string) (string, bool) {
	for _, a := range e.Attrs {
		if a.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// SetAttrNS 设置属性（已存在则更新，否则追加）
func (e *Element) SetAttrNS(ns, local, value string) {
	for i := range e.Attrs {
		if e.Attrs[i].Local == local && e.Attrs[i].NS == ns {
			e.Attrs[i].Value = value
			e.Attrs[i].Name = local
			return
		}
	}
	name := local
	if ns == AndroidNS {
		name = "android:" + local
	} else if ns == ToolsNS {
		name = "tools:" + local
	}
	e.Attrs = append(e.Attrs, Attr{Name: name, NS: ns, Local: local, Value: value})
}

// RemoveAttrNS 删除属性
func (e *Element) RemoveAttrNS(ns, local string) {
	out := e.Attrs[:0]
	for _, a := range e.Attrs {
		if a.Local == local && a.NS == ns {
			continue
		}
		out = append(out, a)
	}
	e.Attrs = out
}

// Clone 深拷贝元素
func (e *Element) Clone() *Element {
	n := &Element{
		Name:      e.Name,
		LocalName: e.LocalName,
		Attrs:     make([]Attr, len(e.Attrs)),
	}
	copy(n.Attrs, e.Attrs)
	for _, c := range e.Children {
		nc := c.Clone()
		nc.Parent = n
		n.Children = append(n.Children, nc)
	}
	return n
}

// RemoveChild 移除子元素 fmt
func (e *Element) RemoveChild(child *Element) {
	out := e.Children[:0]
	for _, c := range e.Children {
		if c == child {
			continue
		}
		out = append(out, c)
	}
	e.Children = out
}

// Marshal 序列化为 XML（带缩进，对齐 ManifestMerger 输出风格）
func (d *Doc) Marshal() string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	if d.Root != nil {
		writeElement(&sb, d.Root, 0)
	}
	return sb.String()
}

func writeElement(sb *strings.Builder, el *Element, depth int) {
	indent := strings.Repeat("    ", depth)
	sb.WriteString(indent)
	sb.WriteString("<")
	sb.WriteString(el.Name)

	// 稳定排序：android: 前缀优先，其余按名字排序
	attrs := make([]Attr, len(el.Attrs))
	copy(attrs, el.Attrs)
	sort.SliceStable(attrs, func(i, j int) bool {
		return attrSortKey(attrs[i]) < attrSortKey(attrs[j])
	})
	for _, a := range attrs {
		sb.WriteString(" ")
		sb.WriteString(a.Name)
		sb.WriteString(`="`)
		sb.WriteString(escapeXML(a.Value, true))
		sb.WriteString(`"`)
	}

	if len(el.Children) == 0 {
		sb.WriteString("/>\n")
		return
	}
	sb.WriteString(">\n")
	for _, c := range el.Children {
		writeElement(sb, c, depth+1)
	}
	sb.WriteString(indent)
	sb.WriteString("</")
	sb.WriteString(el.Name)
	sb.WriteString(">\n")
}

func attrSortKey(a Attr) string {
	if a.NS == AndroidNS {
		return "android:" + a.Local
	}
	if a.NS == ToolsNS {
		return "tools:" + a.Local
	}
	return "zzz:" + a.Name
}

func escapeXML(s string, attribute bool) string {
	var sb strings.Builder
	for _, c := range s {
		switch c {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '"':
			if attribute {
				sb.WriteString("&quot;")
			} else {
				sb.WriteByte('"')
			}
		default:
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

// ChildElements 返回子元素列表（仅元素节点）
func ChildElements(e *Element) []*Element {
	return e.Children
}