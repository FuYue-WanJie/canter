package builder

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mergeValuesXML 合并多个 values XML 文件，按 (tag, name) 去重，先出现的胜出（模块资源优先于依赖）
func mergeValuesXML(srcFiles []string, destFile string) error {
	type node struct {
		tag   string
		attrs map[string]string
		inner []byte
	}

	merged := []node{}
	keyIndex := map[[2]string]int{}

	for _, src := range srcFiles {
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			se, ok := tok.(xml.StartElement)
			if !ok {
				continue
			}
			if se.Name.Local == "resources" {
				continue
			}
			attrs := map[string]string{}
			for _, a := range se.Attr {
				attrs[a.Name.Local] = a.Value
			}
			key := [2]string{se.Name.Local, attrs["name"]}
			n := node{tag: se.Name.Local, attrs: attrs, inner: encodeSubtree(dec)}
			if _, exists := keyIndex[key]; !exists {
				keyIndex[key] = len(merged)
				merged = append(merged, n)
			}
		}
	}

	os.MkdirAll(filepath.Dir(destFile), 0755)
	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n<resources>\n")
	for _, n := range merged {
		out.WriteString("    <" + n.tag)
		keys := make([]string, 0, len(n.attrs))
		for k := range n.attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.WriteString(" " + k + `="` + escapeXML(n.attrs[k]) + `"`)
		}
		if len(n.inner) == 0 {
			out.WriteString("/>\n")
		} else {
			out.WriteString(">" + string(n.inner) + "</" + n.tag + ">\n")
		}
	}
	out.WriteString("</resources>\n")
	return os.WriteFile(destFile, out.Bytes(), 0644)
}

// encodeSubtree 读取当前深度内的完整子树并序列化为字节（EncodeToken 正确处理所有 token 类型）
func encodeSubtree(dec *xml.Decoder) []byte {
	var buf bytes.Buffer
	e := xml.NewEncoder(&buf)
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
		e.EncodeToken(tok)
	}
	e.Flush()
	return buf.Bytes()
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	os.MkdirAll(filepath.Dir(dst), 0755)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// MergeResourceDirs 合并多个资源目录
func MergeResourceDirs(srcDirs []string, destDir string) error {
	os.MkdirAll(destDir, 0755)

	// 阶段 1：非 values 文件直接复制（先到先得）
	for _, src := range srcDirs {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(src, path)
			if rerr != nil {
				return nil
			}
			relSlash := filepath.ToSlash(rel)
			if strings.HasPrefix(relSlash, "values/") || strings.HasPrefix(relSlash, "values-") {
				return nil
			}
			dest := filepath.Join(destDir, rel)
			if _, err := os.Stat(dest); err != nil {
				copyFile(path, dest)
			}
			return nil
		})
	}

	// 阶段 2+3：合并 values 与 values-* 目录（跨依赖全局按条目合并）
	// 先收集每个 (qualifier, filename) 的所有来源文件（按 srcDirs 顺序，模块在后覆盖依赖）
	groupedFiles := map[string][]string{} // key: qualifier/name
	for _, src := range srcDirs {
		entries, err := os.ReadDir(src)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			qualifier := e.Name()
			if qualifier != "values" && !strings.HasPrefix(qualifier, "values-") {
				continue
			}
			qualDir := filepath.Join(src, qualifier)
			files, _ := os.ReadDir(qualDir)
			for _, f := range files {
				if f.IsDir() || !strings.HasSuffix(f.Name(), ".xml") {
					continue
				}
				key := qualifier + "/" + f.Name()
				groupedFiles[key] = append(groupedFiles[key], filepath.Join(qualDir, f.Name()))
			}
		}
	}
	keys := make([]string, 0, len(groupedFiles))
	for k := range groupedFiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		list := groupedFiles[key]
		idx := strings.Index(key, "/")
		qualifier, name := key[:idx], key[idx+1:]
		dest := filepath.Join(destDir, qualifier, name)
		if len(list) == 1 {
			copyFile(list[0], dest)
		} else {
			// 多依赖同名 values 文件：按条目合并，模块（后出现）覆盖依赖
			mergeValuesXML(list, dest)
		}
	}
	return nil
}
