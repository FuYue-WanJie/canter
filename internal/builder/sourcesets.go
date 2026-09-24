package builder

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"canter/internal/engine"
)

// moduleSourceRoots 返回模块的可编译源码根目录（src/main + 选中 flavor 的 java/kotlin 目录）
func moduleSourceRoots(module, selectedFlavor string) []string {
	var roots []string
	seen := map[string]bool{}
	srcRoot := filepath.Join(module, "src")
	_, err := os.ReadDir(srcRoot)
	if err != nil {
		return nil
	}
	// main 优先，其次选中 flavor（如有），其余 flavor 目录跳过避免重复符号
	ordered := []string{"main"}
	if selectedFlavor != "" {
		ordered = append(ordered, selectedFlavor)
	}
	for _, set := range ordered {
		for _, sub := range []string{"java", "kotlin"} {
			r := filepath.Join(srcRoot, set, sub)
			if fi, err := os.Stat(r); err == nil && fi.IsDir() && !seen[r] {
				seen[r] = true
				roots = append(roots, r)
			}
		}
	}
	return roots
}

// moduleSourceRootsCtx 基于构建上下文选择 flavor 后收集源码根
func moduleSourceRootsCtx(module string, ctx *engine.BuildContext) []string {
	flavor := ""
	if ac, ok := ctx.Config.(*AppConfig); ok {
		flavor = ac.SelectedFlavor
	}
	return moduleSourceRoots(module, flavor)
}

// collectSourcesInRoots 收集 roots 下指定扩展名的源码文件
func collectSourcesInRoots(roots []string, suffix string) ([]string, bool) {
	var files []string
	found := false
	for _, root := range roots {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, suffix) {
				files = append(files, path)
				found = true
			}
			return nil
		})
	}
	sort.Strings(files)
	return files, found
}