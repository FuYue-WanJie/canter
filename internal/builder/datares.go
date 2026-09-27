package builder

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"

	"canter/internal/engine"
)

// mergeDataJar 收集依赖 jar（classes.jar 与内嵌 libs/*.jar）中的非 .class 数据条目，
// 同名先到先得。产出供打包阶段经 excludes 过滤后并入 APK。
func mergeDataJar(ctx *engine.BuildContext, jarFile string) error {
	out, err := os.Create(jarFile)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	seen := map[string]bool{}
	addEntry := func(name string, r io.Reader) {
		name = filepath.ToSlash(name)
		if seen[name] || isSignatureLike(name) {
			return
		}
		seen[name] = true
		w, err := zw.Create(name)
		if err != nil {
			return
		}
		io.Copy(w, r)
	}
	addJar := func(jarPath string) {
		zr, err := zip.OpenReader(jarPath)
		if err != nil {
			return
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, ".class") {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				continue
			}
			addEntry(f.Name, rc)
			rc.Close()
		}
	}
	depsDir := filepath.Join(ctx.BuildDir, "deps")
	if entries, err := os.ReadDir(depsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			depDir := filepath.Join(depsDir, e.Name())
			addJar(filepath.Join(depDir, "classes.jar"))
			for _, aux := range depAuxJars(depDir) {
				addJar(aux)
			}
		}
	}
	return zw.Close()
}

// dataJarEntries 列出 merged_data.jar 中通过 excludes 过滤后的条目内容
func dataJarEntries(jarFile string, excludes []string) map[string][]byte {
	out := map[string][]byte{}
	zr, err := zip.OpenReader(jarFile)
	if err != nil {
		return out
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if matchAnyExclude(f.Name, excludes) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = data
	}
	return out
}

// matchAnyExclude 匹配 AGP 风格的打包排除模式（* 不跨目录，** 跨目录）
func matchAnyExclude(name string, excludes []string) bool {
	for _, ex := range excludes {
		if globMatch(ex, name) {
			return true
		}
	}
	return false
}

// globMatch 支持 ** 跨目录的简单通配匹配
func globMatch(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		ok, _ := filepath.Match(pattern, name)
		return ok
	}
	parts := strings.Split(pattern, "**")
	prefix, suffix := parts[0], ""
	if len(parts) > 1 {
		suffix = strings.TrimPrefix(parts[1], "/")
	}
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := name[len(prefix):]
	if suffix == "" {
		return true
	}
	// 后缀需逐目录尝试（** 可匹配零个或多个目录段）
	segments := strings.Split(rest, "/")
	for i := range segments {
		candidate := strings.Join(segments[i:], "/")
		if ok, _ := filepath.Match(suffix, candidate); ok {
			return true
		}
	}
	return false
}
