package parser

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VersionCatalog 版本目录（libs.versions.toml）
type VersionCatalog struct {
	Versions  map[string]string
	Libraries map[string]map[string]string
	Plugins   map[string]map[string]string
	Bundles   map[string][]string

	bomCache map[string]map[[2]string]string
}

// NewVersionCatalog 创建空版本目录
func NewVersionCatalog() *VersionCatalog {
	return &VersionCatalog{
		Versions:  map[string]string{},
		Libraries: map[string]map[string]string{},
		Plugins:   map[string]map[string]string{},
		Bundles:   map[string][]string{},
		bomCache:  map[string]map[[2]string]string{},
	}
}

func normalizeAccessor(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "-", "."), "_", ".")
}

// ResolveVersion 解析 version.ref 引用
func (c *VersionCatalog) ResolveVersion(ref string) string {
	return c.Versions[ref]
}

func (c *VersionCatalog) buildCoordinate(lib map[string]string) (group, name, version string, ok bool) {
	group = lib["group"]
	name = lib["name"]
	moduleStr := lib["module"]
	if moduleStr != "" && (group == "" || name == "") {
		parts := strings.Split(moduleStr, ":")
		if len(parts) >= 2 {
			group, name = parts[0], parts[1]
		}
	}
	versionRef := lib["version.ref"]
	version = lib["version"]
	if versionRef != "" {
		if v := c.ResolveVersion(versionRef); v != "" {
			version = v
		}
	}
	if group != "" && name != "" {
		return group, name, version, true
	}
	return "", "", "", false
}

// ResolveLibrary 解析库访问器，如 'androidx.core.ktx' -> (group, name, version)
func (c *VersionCatalog) ResolveLibrary(accessor string) (group, name, version string, ok bool) {
	normalized := normalizeAccessor(accessor)
	if lib, exists := c.Libraries[normalized]; exists {
		return c.buildCoordinate(lib)
	}
	for key, lib := range c.Libraries {
		if normalizeAccessor(key) == normalized {
			return c.buildCoordinate(lib)
		}
	}
	return "", "", "", false
}

// ResolveBomVersion 从 BOM (platform) 依赖中推断版本
func (c *VersionCatalog) ResolveBomVersion(group, artifact string) string {
	for _, lib := range c.Libraries {
		if lib["group"] == group && lib["name"] == artifact {
			vref := lib["version.ref"]
			if vref != "" {
				return c.ResolveVersion(vref)
			}
		}
	}
	return ""
}

type pomDependency struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
}

type pomDependencyManagement struct {
	Dependencies []pomDependency `xml:"dependencies>dependency"`
}

type pomModel struct {
	XMLName              xml.Name               `xml:"project"`
	GroupID              string                 `xml:"groupId"`
	ArtifactID           string                 `xml:"artifactId"`
	Version              string                 `xml:"version"`
	Packaging            string                 `xml:"packaging"`
	Dependencies         []pomDependency        `xml:"dependencies>dependency"`
	DependencyManagement pomDependencyManagement `xml:"dependencyManagement"`
}

// fetchBomVersions 下载 BOM POM 并解析 dependencyManagement 中的版本映射。
// 优先读本地依赖缓存（~/.minibuild/cache/deps），回退远程镜像。
func (c *VersionCatalog) fetchBomVersions(bomGroup, bomVersion string) map[[2]string]string {
	cacheKey := bomGroup + ":" + bomVersion
	if m, ok := c.bomCache[cacheKey]; ok {
		return m
	}
	if data := readCachedBomPOM(bomGroup, bomVersion); data != nil {
		if m := parsePOMDependencyManagement(data); len(m) > 0 {
			c.bomCache[cacheKey] = m
			return m
		}
	}

	groupPath := strings.ReplaceAll(bomGroup, ".", "/")
	repos := []string{
		"https://maven.aliyun.com/repository/google",
		"https://maven.aliyun.com/repository/central",
		"https://mirrors.huaweicloud.com/repository/maven",
	}

	client := &http.Client{Timeout: 8 * time.Second}
	for _, repo := range repos {
		arts := []string{"compose-bom", bomGroup[strings.LastIndex(bomGroup, ".")+1:]}
		for _, art := range arts {
			url := fmt.Sprintf("%s/%s/%s/%s/%s-%s.pom", repo, groupPath, art, bomVersion, art, bomVersion)
			resp, err := client.Get(url)
			if err != nil {
				continue
			}
			var model pomModel
			if err := xml.NewDecoder(resp.Body).Decode(&model); err != nil {
				resp.Body.Close()
				continue
			}
			resp.Body.Close()
			mapping := map[[2]string]string{}
			for _, dep := range model.DependencyManagement.Dependencies {
				if dep.GroupID != "" && dep.ArtifactID != "" && dep.Version != "" {
					mapping[[2]string{dep.GroupID, dep.ArtifactID}] = dep.Version
				}
			}
			c.bomCache[cacheKey] = mapping
			return mapping
		}
	}
	c.bomCache[cacheKey] = map[[2]string]string{}
	return c.bomCache[cacheKey]
}

// readCachedBomPOM 从本地依赖缓存读取 BOM POM（返回 nil 表示未缓存）
func readCachedBomPOM(bomGroup, bomVersion string) []byte {
	if bomGroup == "" || bomVersion == "" {
		return nil
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	groupPath := strings.ReplaceAll(bomGroup, ".", "/")
	base := filepath.Join(home, ".minibuild", "cache", "deps", filepath.FromSlash(groupPath))
	for _, art := range []string{"compose-bom", bomGroup[strings.LastIndex(bomGroup, ".")+1:]} {
		p := filepath.Join(base, art, bomVersion, art+"-"+bomVersion+".pom")
		if data, err := os.ReadFile(p); err == nil {
			return data
		}
	}
	return nil
}

// parsePOMDependencyManagement 解析 POM 的 dependencyManagement 版本映射
func parsePOMDependencyManagement(data []byte) map[[2]string]string {
	var model pomModel
	if err := xml.Unmarshal(data, &model); err != nil {
		return nil
	}
	if len(model.DependencyManagement.Dependencies) == 0 {
		return nil
	}
	mapping := map[[2]string]string{}
	for _, dep := range model.DependencyManagement.Dependencies {
		if dep.GroupID != "" && dep.ArtifactID != "" && dep.Version != "" {
			mapping[[2]string{dep.GroupID, dep.ArtifactID}] = dep.Version
		}
	}
	return mapping
}

// BomConstraints 返回某 BOM 的 dependencyManagement 约束映射，key 为 "group:artifact"
func (c *VersionCatalog) BomConstraints(bomGroup, bomVersion string) map[string]string {
	m := c.fetchBomVersions(bomGroup, bomVersion)
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k[0]+":"+k[1]] = v
	}
	return out
}

// ResolveWithBom 解析库并尝试从 BOM POM 推断真实版本
func (c *VersionCatalog) ResolveWithBom(accessor string, boms map[string]string) (group, name, version string, ok bool) {
	group, name, version, ok = c.ResolveLibrary(accessor)
	if !ok {
		return "", "", "", false
	}
	if version == "" && boms != nil {
		for bomGroup, bomVersion := range boms {
			if strings.HasPrefix(group, bomGroup) {
				if inferred := c.ResolveBomVersion(group, name); inferred != "" {
					return group, name, inferred, true
				}
				bomMapping := c.fetchBomVersions(bomGroup, bomVersion)
				if v, exists := bomMapping[[2]string{group, name}]; exists {
					return group, name, v, true
				}
				if v, exists := bomMapping[[2]string{group, name + "-android"}]; exists {
					return group, name + "-android", v, true
				}
				return group, name, bomVersion, true
			}
		}
	}
	return group, name, version, true
}

// ResolvePlugin 解析插件访问器 -> (id, version)
func (c *VersionCatalog) ResolvePlugin(accessor string) (id, version string, ok bool) {
	normalized := normalizeAccessor(accessor)
	for key, plugin := range c.Plugins {
		if normalizeAccessor(key) == normalized {
			pluginID := plugin["id"]
			versionRef := plugin["version.ref"]
			if versionRef == "" {
				versionRef = plugin["version"]
			}
			v := c.ResolveVersion(versionRef)
			if v == "" {
				v = versionRef
			}
			return pluginID, v, true
		}
	}
	return "", "", false
}
