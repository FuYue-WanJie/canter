package builder

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// unzipDepTo 把已下载的依赖（aar/jar）解压到 depsDir/<group_artifact>/
func unzipDepTo(group, artifact, path, depsDir string) {
	depDir := filepath.Join(depsDir, depDirName(group, artifact))
	os.MkdirAll(depDir, 0755)
	if strings.HasSuffix(path, ".aar") {
		copyFile(path, filepath.Join(depDir, filepath.Base(path)))
		if zr, err := zip.OpenReader(path); err == nil {
			for _, f := range zr.File {
				if f.FileInfo().IsDir() {
					continue
				}
				if f.Name == "classes.jar" {
					extractZipEntry(f, filepath.Join(depDir, "classes.jar"))
				} else if strings.HasPrefix(f.Name, "res/") {
					extractZipEntry(f, filepath.Join(depDir, f.Name))
				} else if strings.HasPrefix(f.Name, "libs/") && strings.HasSuffix(f.Name, ".jar") {
					// AAR 内嵌 jar（如 emoji2 的 libs/repackaged.jar）需并入 classpath 与 dex
					extractZipEntry(f, filepath.Join(depDir, f.Name))
				} else if f.Name == "proguard.txt" {
					// AAR consumer proguard 规则，release 构建时并入 R8
					extractZipEntry(f, filepath.Join(depDir, "proguard.txt"))
				}
			}
			zr.Close()
		}
	} else if strings.HasSuffix(path, ".jar") {
		// 保留 classes.jar 副本供编译 classpath 使用
		copyFile(path, filepath.Join(depDir, "classes.jar"))
		if zr, err := zip.OpenReader(path); err == nil {
			for _, f := range zr.File {
				if f.FileInfo().IsDir() {
					continue
				}
				if strings.HasSuffix(f.Name, ".class") {
					extractZipEntry(f, filepath.Join(depDir, f.Name))
				}
			}
			zr.Close()
		}
	}
}

func extractZipEntry(f *zip.File, dst string) {
	rc, err := f.Open()
	if err != nil {
		return
	}
	defer rc.Close()
	os.MkdirAll(filepath.Dir(dst), 0755)
	out, err := os.Create(dst)
	if err != nil {
		return
	}
	io.Copy(out, rc)
	out.Close()
}

// depAuxJars 返回依赖目录下 AAR 内嵌 jar（libs/*.jar）的路径列表
func depAuxJars(depDir string) []string {
	entries, err := os.ReadDir(filepath.Join(depDir, "libs"))
	if err != nil {
		return nil
	}
	var jars []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
			jars = append(jars, filepath.Join(depDir, "libs", e.Name()))
		}
	}
	return jars
}

// depConsumerRules 返回依赖目录下 AAR consumer proguard 规则（proguard.txt）的路径列表
func depConsumerRules(depsDir string) []string {
	entries, err := os.ReadDir(depsDir)
	if err != nil {
		return nil
	}
	var rules []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(depsDir, e.Name(), "proguard.txt")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			rules = append(rules, p)
		}
	}
	sort.Strings(rules)
	return rules
}

// materializeDeps 从缓存坐标下载并解压依赖（下载命中全局缓存时很快）
func (b *Builder) materializeDeps(coords []depCoord, depsDir string, downloader *Downloader) error {
	success, total := 0, 0
	var failed []string
	for _, c := range coords {
		total++
		artPath, err := downloader.Download(c.Group, c.Artifact, c.Version)
		if err != nil {
			failed = append(failed, c.Group+":"+c.Artifact+":"+c.Version)
			continue
		}
		success++
		unzipDepTo(c.Group, c.Artifact, artPath, depsDir)
	}
	fmt.Printf("依赖解析: 成功 %d/%d\n", success, total)
	if len(failed) > 0 {
		max := len(failed)
		if max > 10 {
			max = 10
		}
		fmt.Printf("  失败 %d 个: %v%s\n", len(failed), failed[:max], map[bool]string{true: " ...", false: ""}[len(failed) > 10])
	}
	return nil
}
