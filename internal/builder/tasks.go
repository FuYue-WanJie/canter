package builder

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"canter/internal/engine"
)

// AppConfig 应用配置（由 Builder 从 ProjectConfig 提取）
type AppConfig struct {
	Namespace      string
	ApplicationID  string
	VersionCode    string
	VersionName    string
	MinSDK         string
	TargetSDK      string
	CompileSDK     string
	JvmTarget      string
	ABIFilters     []string
	ModuleName     string
	ModuleDir      string
	MinifyEnabled  bool
	ProguardFiles  []string
	Release        bool
	BuildConfigs   []string // flavor buildConfigField 等额外字段（key=value）
	SelectedFlavor string
	Parcelize      bool     // 是否启用 kotlin-parcelize 插件
	Compose        bool     // 是否启用 Compose 编译器插件
	VersionNameSuffix string // flavor 的 versionNameSuffix
	LibraryResDirs []string // 项目 library 模块的 res 目录
	LibraryAssets  []string // 项目 library 模块的 assets 目录
	LibraryJniLibs []string // 项目 library 模块的 jniLibs 目录
}

// MinifyCompatible 判断是否启用 R8 混淆
func (c *AppConfig) MinifyCompatible() bool {
	return c.MinifyEnabled && c.Release
}

func buildClasspath(ctx *engine.BuildContext) string {
	var parts []string
	depsDir := filepath.Join(ctx.BuildDir, "deps")
	if entries, err := os.ReadDir(depsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				parts = append(parts, filepath.Join(depsDir, e.Name(), "classes.jar"))
			}
		}
	}
	// project 库模块的编译输出
	for _, d := range projectLibClassDirs(ctx) {
		parts = append(parts, d)
	}
	home, _ := os.UserHomeDir()
	gradleCache := filepath.Join(home, ".gradle", "caches")
	filepath.Walk(gradleCache, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, "kotlin-stdlib-") && strings.HasSuffix(name, ".jar") &&
			!strings.Contains(name, "sources") && !strings.Contains(name, "javadoc") {
			parts = append(parts, path)
		}
		return nil
	})
	return strings.Join(parts, ":")
}

var gradleClassDirs = []string{
	"build/intermediates/built_in_kotlinc/liteDebug/compileLiteDebugKotlin/classes",
	"build/intermediates/built_in_kotlinc/fullDebug/compileFullDebugKotlin/classes",
	"build/intermediates/built_in_kotlinc/liteDebug/compileFullDebugKotlin/classes",
	"build/tmp/kotlin-classes/liteDebug",
	"build/tmp/kotlin-classes/fullDebug",
	"build/tmp/kotlin-classes/debug",
	"build/intermediates/javac/liteDebug/classes",
	"build/intermediates/javac/fullDebug/classes",
	"build/intermediates/javac/debug/classes",
}

func copyGradleClasses(gradleDir, output string) bool {
	output = filepath.Join(output)
	tmpOut := output + ".tmp"
	os.RemoveAll(tmpOut)

	found := false
	for _, cand := range gradleClassDirs {
		src := filepath.Join(gradleDir, filepath.FromSlash(cand))
		if info, err := os.Stat(src); err == nil && info.IsDir() {
			if !found {
				copyTree(src, tmpOut)
				found = true
			} else {
				// 额外 class 复制进 tmpOut
				filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() {
						return nil
					}
					if strings.HasSuffix(path, ".class") {
						rel, _ := filepath.Rel(src, path)
						dst := filepath.Join(tmpOut, rel)
						os.MkdirAll(filepath.Dir(dst), 0755)
						copyFile(path, dst)
					}
					return nil
				})
			}
		}
	}
	if !found {
		// 回退 build/classes/kotlin/debug
		src := filepath.Join(gradleDir, "build", "classes", "kotlin", "debug")
		if info, err := os.Stat(src); err == nil && info.IsDir() {
			copyTree(src, tmpOut)
			found = true
		}
	}
	if !found {
		return false
	}
	os.RemoveAll(output)
	return os.Rename(tmpOut, output) == nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}
// DexBuildTask D8 打包（合并工程类与依赖类后一次 D8，支持 multidex）
func DexBuildTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("dexBuild")
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "kotlin_classes"))
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "classes"))
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "libs"))
	t.AddFileInputs(depsSignatureFile(ctx))
	t.AddDirOutputs(filepath.Join(ctx.BuildDir, "dex"))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		fmt.Println("D8 打包...")
		d8 := filepath.Join(ctx.BuildTools, "d8")
		if _, err := os.Stat(d8); err != nil {
			fmt.Printf("d8 未找到: %s\n", d8)
			return false
		}

		merged := filepath.Join(ctx.BuildDir, "merged_classes")
		kotlinClasses := filepath.Join(ctx.BuildDir, "kotlin_classes")
		javaClasses := filepath.Join(ctx.BuildDir, "classes")
		depsDir := filepath.Join(ctx.BuildDir, "deps")

		// 每次都完整重建 merged_classes，避免陈旧/重复类残留
		tmp := merged + ".tmp"
		os.RemoveAll(tmp)
		if err := copyTree(javaClasses, tmp); err != nil {
			fmt.Printf("复制 Java 类失败: %v\n", err)
			return false
		}
		if err := copyTree(kotlinClasses, tmp); err != nil {
			fmt.Printf("复制 Kotlin 类失败: %v\n", err)
			return false
		}
		// 项目 library 模块的编译产物（build/libs/*/classes）
		for _, libDir := range projectLibClassDirs(ctx) {
			if err := copyTree(libDir, tmp); err != nil {
				fmt.Printf("复制库模块类失败: %v\n", err)
			}
		}
		// 依赖类：同名类先到先得（避免 KMP 变体重复）
		if entries, err := os.ReadDir(depsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				classesJar := filepath.Join(depsDir, e.Name(), "classes.jar")
				if zr, err := zip.OpenReader(classesJar); err == nil {
					for _, f := range zr.File {
						if !strings.HasSuffix(f.Name, ".class") {
							continue
						}
						dest := filepath.Join(tmp, f.Name)
						if _, err := os.Stat(dest); err == nil {
							continue // 已存在则跳过，避免重复类
						}
						os.MkdirAll(filepath.Dir(dest), 0755)
						rc, err := f.Open()
						if err != nil {
							continue
						}
						out, err := os.Create(dest)
						if err == nil {
							io.Copy(out, rc)
							out.Close()
						}
						rc.Close()
					}
					zr.Close()
				}
			}
		}
		os.RemoveAll(merged)
		if err := os.Rename(tmp, merged); err != nil {
			fmt.Printf("合并类目录失败: %v\n", err)
			return false
		}

		// 打包 merged_classes.jar
		jarFile := filepath.Join(ctx.BuildDir, "merged_classes.jar")
		out, err := os.Create(jarFile)
		if err != nil {
			fmt.Printf("创建 jar 失败: %v\n", err)
			return false
		}
		zw := zip.NewWriter(out)
		filepath.Walk(merged, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(merged, path)
			fh, err := zw.Create(filepath.ToSlash(rel))
			if err != nil {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			fh.Write(data)
			return nil
		})
		zw.Close()
		out.Close()

		dexDir := filepath.Join(ctx.BuildDir, "dex")
		os.RemoveAll(dexDir)
		os.MkdirAll(dexDir, 0755)
		minAPI := "23"
		if ac, ok := ctx.Config.(*AppConfig); ok && ac.MinSDK != "" {
			minAPI = ac.MinSDK
		}
		args := []string{"--lib", ctx.AndroidJar, "--min-api", minAPI, "--output", dexDir, jarFile}
		if err := runD8(d8, args); err != nil {
			fmt.Printf("D8 失败: %s\n", err)
			return false
		}
		return true
	}
	return t
}
// PackageApkTask 打包 APK
func PackageApkTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("packageApk")
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "dex"))
	t.AddFileInputs(depsSignatureFile(ctx))
	t.AddFileInputs(filepath.Join(ctx.BuildDir, "resources.ap_"))
	t.AddFileOutputs(filepath.Join(ctx.BuildDir, "app-debug.apk"))
	if ac, ok := ctx.Config.(*AppConfig); ok {
		for _, d := range ac.LibraryAssets {
			t.AddDirInputs(d)
		}
		for _, d := range ac.LibraryJniLibs {
			t.AddDirInputs(d)
		}
	}
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		tmpDir, err := os.MkdirTemp("", "canter-apk")
		if err != nil {
			fmt.Printf("创建临时目录失败: %v\n", err)
			return false
		}
		defer os.RemoveAll(tmpDir)

		// 1. 复制 dex
		dexDir := filepath.Join(ctx.BuildDir, "dex")
		if entries, err := os.ReadDir(dexDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".dex") {
					copyFile(filepath.Join(dexDir, e.Name()), filepath.Join(tmpDir, e.Name()))
				}
			}
		}

		// 2. 解压 resources.ap_
		resAP := filepath.Join(ctx.BuildDir, "resources.ap_")
		if zr, err := zip.OpenReader(resAP); err == nil {
			for _, f := range zr.File {
				dst := filepath.Join(tmpDir, f.Name)
				if f.FileInfo().IsDir() {
					os.MkdirAll(dst, 0755)
					continue
				}
				os.MkdirAll(filepath.Dir(dst), 0755)
				rc, err := f.Open()
				if err == nil {
					out, err := os.Create(dst)
					if err == nil {
						io.Copy(out, rc)
						out.Close()
					}
					rc.Close()
				}
			}
			zr.Close()
		}

		// 3. 处理依赖 AAR 的 jni/assets
		// ABI 过滤：ndk.abiFilters 显式指定时才过滤；否则包含全部 ABI（对齐 Gradle universal APK）
		var abiFilters []string
		if cfg, ok := ctx.Config.(*AppConfig); ok && len(cfg.ABIFilters) > 0 {
			abiFilters = cfg.ABIFilters
		}
		abiIncluded := func(abi string) bool {
			if len(abiFilters) == 0 {
				return true
			}
			return contains(abiFilters, abi)
		}
		// 3a. app 自身与 library 模块的 assets / jniLibs
		if cfg, ok := ctx.Config.(*AppConfig); ok {
			for _, adir := range cfg.LibraryAssets {
				copyTree(adir, filepath.Join(tmpDir, "assets"))
			}
			for _, jdir := range cfg.LibraryJniLibs {
				entries, _ := os.ReadDir(jdir)
				for _, e := range entries {
					if e.IsDir() && abiIncluded(e.Name()) {
						copyTree(filepath.Join(jdir, e.Name()), filepath.Join(tmpDir, "lib", e.Name()))
					}
				}
			}
		}
		depsDir := filepath.Join(ctx.BuildDir, "deps")
		filepath.Walk(depsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".aar") {
				return nil
			}
			zr, err := zip.OpenReader(path)
			if err != nil {
				return nil
			}
			for _, f := range zr.File {
				if f.FileInfo().IsDir() {
					continue
				}
				if strings.HasPrefix(f.Name, "jni/") && strings.HasSuffix(f.Name, ".so") {
					parts := strings.SplitN(f.Name, "/", 3)
					if len(parts) >= 2 {
						abi := parts[1]
						if abiIncluded(abi) {
							rc, _ := f.Open()
							dst := filepath.Join(tmpDir, "lib", abi, filepath.Base(f.Name))
							os.MkdirAll(filepath.Dir(dst), 0755)
							out, _ := os.Create(dst)
							if out != nil {
								io.Copy(out, rc)
								out.Close()
							}
							rc.Close()
						}
					}
				} else if strings.HasPrefix(f.Name, "assets/") {
					rc, _ := f.Open()
					dst := filepath.Join(tmpDir, f.Name)
					os.MkdirAll(filepath.Dir(dst), 0755)
					out, _ := os.Create(dst)
					if out != nil {
						io.Copy(out, rc)
						out.Close()
					}
					rc.Close()
				}
			}
			zr.Close()
			return nil
		})

		// 4. 打包 APK
		apkPath := filepath.Join(ctx.BuildDir, "app-debug.apk")
		out, err := os.Create(apkPath)
		if err != nil {
			fmt.Printf("创建 APK 失败: %v\n", err)
			return false
		}
		zw := zip.NewWriter(out)
		filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			relPath, rerr := filepath.Rel(tmpDir, path)
			if rerr != nil {
				return nil
			}
			rel := filepath.ToSlash(relPath)
			// native 库不压缩（对齐 AGP：便于运行时 mmap，且与 Gradle 产物一致）
			var fh io.Writer
			if strings.HasPrefix(rel, "lib/") && strings.HasSuffix(rel, ".so") {
				w, err := zw.CreateHeader(&zip.FileHeader{Name: rel, Method: zip.Store})
				if err != nil {
					return nil
				}
				fh = w
			} else {
				w, err := zw.Create(rel)
				if err != nil {
					return nil
				}
				fh = w
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			fh.Write(data)
			return nil
		})
		zw.Close()
		out.Close()

		if info, err := os.Stat(apkPath); err == nil {
			fmt.Printf("APK: %s (%dKB)\n", apkPath, info.Size()/1024)
		}
		return true
	}
	return t
}

// SignApkTask 签名 APK
func SignApkTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("signApk")
	t.AddFileInputs(filepath.Join(ctx.BuildDir, "app-debug.apk"))
	t.AddFileOutputs(filepath.Join(ctx.BuildDir, "app-signed.apk"))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		apksigner := filepath.Join(ctx.BuildTools, "apksigner")
		appDebug := filepath.Join(ctx.BuildDir, "app-debug.apk")
		appSigned := filepath.Join(ctx.BuildDir, "app-signed.apk")
		home, _ := os.UserHomeDir()
		keystore := filepath.Join(home, ".android", "debug.keystore")

		if _, err := os.Stat(apksigner); err != nil {
			fmt.Println("警告: apksigner 未找到，跳过签名")
			copyFile(appDebug, appSigned)
			return true
		}
		if _, err := os.Stat(keystore); err != nil {
			fmt.Println("警告: debug.keystore 未找到，跳过签名")
			copyFile(appDebug, appSigned)
			return true
		}

		args := []string{
			"sign",
			"--ks", keystore,
			"--ks-pass", "pass:android",
			"--key-pass", "pass:android",
			"--out", appSigned,
			appDebug,
		}
		cmd := exec.Command(apksigner, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("签名失败: %s\n", string(out))
			return false
		}
		return true
	}
	return t
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
