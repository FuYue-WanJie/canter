package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// depCoord 依赖坐标
type depCoord struct {
	Group    string `json:"group"`
	Artifact string `json:"artifact"`
	Version  string `json:"version"`
}

// resolutionCache 依赖解析结果缓存
type resolutionCache struct {
	Key  string     `json:"key"`
	Deps []depCoord `json:"deps"`
}

// resolutionKey 计算解析缓存键（直接依赖 + BOM 约束 + kotlin 版本）
func resolutionKey(direct []depCoord, bomConstraints map[string]string, kotlinVersion string) string {
	h := sha256.New()
	keys := make([]string, 0, len(direct))
	for _, d := range direct {
		keys = append(keys, d.Group+":"+d.Artifact+":"+d.Version)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k + "\n"))
	}
	h.Write([]byte("#bom\n"))
	bk := make([]string, 0, len(bomConstraints))
	for k, v := range bomConstraints {
		bk = append(bk, k+"="+v)
	}
	sort.Strings(bk)
	for _, k := range bk {
		h.Write([]byte(k + "\n"))
	}
	h.Write([]byte("#kotlin=" + kotlinVersion))
	return hex.EncodeToString(h.Sum(nil))
}

// loadResolutionCache 读取解析缓存（键匹配才有效）
func loadResolutionCache(cacheDir, key string) ([]depCoord, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir, "deps-resolution.json"))
	if err != nil {
		return nil, false
	}
	var c resolutionCache
	if err := json.Unmarshal(data, &c); err != nil || c.Key != key {
		return nil, false
	}
	return c.Deps, true
}

// saveResolutionCache 写入解析缓存
func saveResolutionCache(cacheDir, key string, deps []depCoord) {
	os.MkdirAll(cacheDir, 0755)
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].Group != deps[j].Group {
			return deps[i].Group < deps[j].Group
		}
		return deps[i].Artifact < deps[j].Artifact
	})
	c := resolutionCache{Key: key, Deps: deps}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(cacheDir, "deps-resolution.json"), data, 0644)
}

// depDirName 依赖解压目录名
func depDirName(group, artifact string) string {
	return strings.ReplaceAll(group+"_"+artifact, ".", "_")
}