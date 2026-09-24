package builder

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// materializeDeps 从缓存坐标下载并解压依赖（下载命中全局缓存时很快）
func (b *Builder) materializeDeps(coords []depCoord, depsDir string, downloader *Downloader) error {
	success, total := 0, 0
	for _, c := range coords {
		total++
		artPath, err := downloader.Download(c.Group, c.Artifact, c.Version)
		if err != nil {
			continue
		}
		success++
		unzipDepTo(c.Group, c.Artifact, artPath, depsDir)
	}
	fmt.Printf("依赖解析: 成功 %d/%d\n", success, total)
	return nil
}