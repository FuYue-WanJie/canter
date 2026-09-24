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
func loadResolutionCache(cacheFile, key string) ([]depCoord, bool) {
	data, err := os.ReadFile(cacheFile)
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
func saveResolutionCache(cacheFile, key string, deps []depCoord) {
	os.MkdirAll(filepath.Dir(cacheFile), 0755)
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
	os.WriteFile(cacheFile, data, 0644)
}

// depDirName 依赖解压目录名
func depDirName(group, artifact string) string {
	return strings.ReplaceAll(group+"_"+artifact, ".", "_")
}

// coordHash 计算依赖坐标集合的哈希（用于解压标记失效判断）
func coordHash(coords []depCoord) string {
	h := sha256.New()
	keys := make([]string, 0, len(coords))
	for _, c := range coords {
		keys = append(keys, c.Group+":"+c.Artifact+":"+c.Version)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// collapseVariantCoords 折叠 KMP 平台变体：同 base 名只保留一个（优先 -android > 无后缀 > -release > -jvm）
func collapseVariantCoords(coords []depCoord) []depCoord {
	baseOf := func(a string) string {
		for _, s := range []string{"-android", "-jvm", "-release", "-desktop", "-linuxx64", "-macosx64", "-macosarm64", "-windows", "-wasm", "-js", "-metadata"} {
			if strings.HasSuffix(a, s) {
				return a[:len(a)-len(s)]
			}
		}
		return a
	}
	scoreOf := func(a string) int {
		switch {
		case strings.HasSuffix(a, "-android"):
			return 0
		case baseOf(a) == a:
			return 1
		case strings.HasSuffix(a, "-release"):
			return 2
		case strings.HasSuffix(a, "-jvm"):
			return 3
		default:
			return 4
		}
	}
	best := map[string]depCoord{}
	for _, c := range coords {
		base := c.Group + ":" + baseOf(c.Artifact)
		if cur, ok := best[base]; !ok || scoreOf(c.Artifact) < scoreOf(cur.Artifact) {
			best[base] = c
		}
	}
	out := make([]depCoord, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Artifact < out[j].Artifact
	})
	return out
}