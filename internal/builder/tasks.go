package builder

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"canter/internal/engine"
	"canter/internal/parser"
)

// AppConfig 应用配置（由 Builder 从 ProjectConfig 提取）
type AppConfig struct {
	Namespace          string
	ApplicationID      string
	VersionCode        string
	VersionName        string
	MinSDK             string
	TargetSDK          string
	CompileSDK         string
	JvmTarget          string
	ABIFilters         []string
	ModuleName         string
	ModuleDir          string
	MinifyEnabled      bool
	ShrinkResources    bool // release 构建时的资源收缩（isShrinkResources）
	ProguardFiles      []string
	Release            bool
	BuildConfigs       []string // flavor buildConfigField 等额外字段（key=value）
	SelectedFlavor     string
	Parcelize          bool   // 是否启用 kotlin-parcelize 插件
	Compose            bool   // 是否启用 Compose 编译器插件
	Serialization      bool   // 是否启用 kotlin-serialization 插件
	VersionNameSuffix  string // flavor 的 versionNameSuffix
	SplitABIEnable     bool   // splits.abi.isEnable
	SplitABIUniversal  bool   // universal APK（splits.abi.isUniversalApk，默认 true）
	SplitABIInclude    []string
	SigningConfigs     map[string]parser.SigningConfigEntry
	SigningConfig      string // release 使用的签名配置名
	VariantConfigs     []parser.VariantConfig
	LocaleFilters      []string
	UseLegacyPackaging bool
	ResourceExcludes   []string
	PackagingExcludes  []string // android 块级 packaging.resources.excludes（全变体）
	LibraryResDirs     []string // 项目 library 模块的 res 目录
	LibraryAssets      []string // 项目 library 模块的 assets 目录
	LibraryJniLibs     []string // 项目 library 模块的 jniLibs 目录
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
				depDir := filepath.Join(depsDir, e.Name())
				parts = append(parts, filepath.Join(depDir, "classes.jar"))
				parts = append(parts, depAuxJars(depDir)...)
			}
		}
	}
	// project 库模块的编译输出
	for _, d := range projectLibClassDirs(ctx) {
		parts = append(parts, d)
	}
	// Kotlin 标准库：严格使用项目声明的 Kotlin 版本（单一版本）
	if stdlib := ToolchainFor(ctx).KotlinStdlibJar(); stdlib != "" {
		parts = append(parts, stdlib)
	}
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

// MergeClassesTask 生成 merged_classes.jar（工程类 + 依赖类，同名先到先得）
// 与 merged_data.jar（依赖 jar 中的数据资源：META-INF/*.version、services、
// kotlin_builtins 等，对齐 AGP 的 javaRes 合并）。D8 与 R8 两条路径共用。
func MergeClassesTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("mergeClasses")
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "kotlin_classes"))
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "classes"))
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "libs"))
	t.AddFileInputs(depsSignatureFile(ctx))
	t.AddFileInputs(packagingConfigFile(ctx))
	t.AddFileOutputs(filepath.Join(ctx.BuildDir, "merged_classes.jar"))
	t.AddFileOutputs(filepath.Join(ctx.BuildDir, "merged_data.jar"))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		if err := mergeClassesJar(ctx, filepath.Join(ctx.BuildDir, "merged_classes.jar")); err != nil {
			fmt.Printf("合并类失败: %v\n", err)
			return false
		}
		if err := mergeDataJar(ctx, filepath.Join(ctx.BuildDir, "merged_data.jar")); err != nil {
			fmt.Printf("合并数据资源失败: %v\n", err)
			return false
		}
		return true
	}
	return t
}

func DexBuildTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("dexBuild")
	t.AddFileInputs(filepath.Join(ctx.BuildDir, "merged_classes.jar"))
	t.AddDirOutputs(filepath.Join(ctx.BuildDir, "dex"))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		fmt.Println("D8 打包...")
		d8 := filepath.Join(ctx.BuildTools, "d8")
		if _, err := os.Stat(d8); err != nil {
			fmt.Printf("d8 未找到: %s\n", d8)
			return false
		}

		jarFile := filepath.Join(ctx.BuildDir, "merged_classes.jar")
		if err := mergeClassesJar(ctx, jarFile); err != nil {
			fmt.Printf("合并类失败: %v\n", err)
			return false
		}

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

// mergeClassesJar 单遍流式合并工程类与依赖类到 jar，同名类先到先得（避免 KMP 变体重复）。
// 相比“解压到目录再打包”，避免产生上万个中间小文件，减少磁盘 I/O。
func mergeClassesJar(ctx *engine.BuildContext, jarFile string) error {
	out, err := os.Create(jarFile)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	seen := map[string]bool{}
	addEntry := func(name string, r io.Reader) {
		name = filepath.ToSlash(name)
		if seen[name] {
			return
		}
		seen[name] = true
		fh, err := zw.Create(name)
		if err != nil {
			return
		}
		io.Copy(fh, r)
	}
	addDir := func(root string) {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".class") {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return nil
			}
			f, oerr := os.Open(path)
			if oerr != nil {
				return nil
			}
			addEntry(rel, f)
			f.Close()
			return nil
		})
	}
	addJar := func(jarPath string) {
		zr, err := zip.OpenReader(jarPath)
		if err != nil {
			return
		}
		defer zr.Close()
		for _, f := range zr.File {
			if !strings.HasSuffix(f.Name, ".class") {
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

	// 工程类优先，随后依赖类
	addDir(filepath.Join(ctx.BuildDir, "classes"))
	addDir(filepath.Join(ctx.BuildDir, "kotlin_classes"))
	for _, libDir := range projectLibClassDirs(ctx) {
		addDir(libDir)
	}
	depsDir := filepath.Join(ctx.BuildDir, "deps")
	if entries, err := os.ReadDir(depsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			depDir := filepath.Join(depsDir, e.Name())
			addJar(filepath.Join(depDir, "classes.jar"))
			// AAR 内嵌 jar（如 emoji2 的 libs/repackaged.jar）
			for _, aux := range depAuxJars(depDir) {
				addJar(aux)
			}
		}
	}
	return zw.Close()
}

// apkVariant 一个 APK 产出计划（universal 或 per-ABI）
type apkVariant struct {
	Name string // 输出基础名（如 "app-debug"、"app-arm64-v8a-debug"）
	ABI  string // 为空表示不过滤（universal）
}

// apkVariants 依据 splits.abi 配置返回本次构建的 APK 产出计划
func apkVariants(cfg *AppConfig) []apkVariant {
	variants := []apkVariant{{Name: "app-debug"}}
	if cfg == nil || !cfg.SplitABIEnable || len(cfg.SplitABIInclude) == 0 {
		return variants
	}
	if !cfg.SplitABIUniversal {
		// 关闭 universal 时仅产 per-ABI（对齐 AGP）
		variants = nil
	}
	for _, abi := range cfg.SplitABIInclude {
		variants = append(variants, apkVariant{Name: "app-" + abi + "-debug", ABI: abi})
	}
	return variants
}

// dataRulesVersion 数据资源默认排除规则的版本（规则变化时自动使 mergeClasses 缓存失效）
const dataRulesVersion = 3

// isSignatureLike 排除不该进 APK 的 jar 条目（各 jar 自带的清单与签名、
// Kotlin module 元数据、proguard 规则与 maven 元数据——对齐 AGP 默认排除集）
func isSignatureLike(name string) bool {
	if name == "module-info.class" {
		return true
	}
	if !strings.HasPrefix(name, "META-INF/") {
		return false
	}
	upper := strings.ToUpper(name)
	for _, ext := range []string{".SF", ".RSA", ".DSA", ".EC", ".LIST"} {
		if strings.HasSuffix(upper, ext) {
			return true
		}
	}
	base := filepath.Base(name)
	switch {
	case base == "MANIFEST.MF":
		return true // 含 META-INF/versions/*/OSGI-INF/MANIFEST.MF
	case strings.HasSuffix(name, ".kotlin_module"):
		return true
	}
	for _, prefix := range []string{
		"META-INF/proguard/",
		"META-INF/com.android.tools/",
		"META-INF/maven/",
		"META-INF/versions/",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// packagingConfigFile 打包配置标记文件：内容随 excludes/localeFilters/
// legacyPackaging 变化，作为打包与签名任务的输入参与签名
func packagingConfigFile(ctx *engine.BuildContext) string {
	h := sha256.New()
	cfg, _ := ctx.Config.(*AppConfig)
	fmt.Fprintf(h, "rules=%d\n", dataRulesVersion)
	if cfg != nil {
		for _, e := range sortedStrings(cfg.ResourceExcludes) {
			fmt.Fprintf(h, "ex=%s\n", e)
		}
		for _, l := range sortedStrings(cfg.LocaleFilters) {
			fmt.Fprintf(h, "locale=%s\n", l)
		}
		fmt.Fprintf(h, "legacy=%v\n", cfg.UseLegacyPackaging)
	}
	path := filepath.Join(ctx.BuildDir, ".packaging-config")
	os.MkdirAll(ctx.BuildDir, 0755)
	if old, err := os.ReadFile(path); err != nil || string(old) != string(h.Sum(nil)) {
		os.WriteFile(path, h.Sum(nil), 0644)
	}
	return path
}

func sortedStrings(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

// PackageApkTask 打包 APK
func PackageApkTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("packageApk")
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "dex"))
	t.AddFileInputs(depsSignatureFile(ctx))
	t.AddFileInputs(filepath.Join(ctx.BuildDir, "resources.ap_"))
	t.AddFileInputs(packagingConfigFile(ctx))
	if cfg, ok := ctx.Config.(*AppConfig); ok {
		for _, v := range apkVariants(cfg) {
			t.AddFileOutputs(filepath.Join(ctx.BuildDir, v.Name+".apk"))
		}
	} else {
		t.AddFileOutputs(filepath.Join(ctx.BuildDir, "app-debug.apk"))
	}
	if ac, ok := ctx.Config.(*AppConfig); ok {
		for _, d := range ac.LibraryAssets {
			t.AddDirInputs(d)
		}
		for _, d := range ac.LibraryJniLibs {
			t.AddDirInputs(d)
		}
	}
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		cfg, _ := ctx.Config.(*AppConfig)
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
		// 资源收缩（release）：把 R8 收缩后的 proto 资源包转回二进制格式
		shrunk := filepath.Join(ctx.BuildDir, "resources-shrunk.zip")
		if _, err := os.Stat(shrunk); err == nil {
			if cfg, ok := ctx.Config.(*AppConfig); ok && cfg.Release && cfg.ShrinkResources {
				aapt2 := filepath.Join(ctx.BuildTools, "aapt2")
				cout, cerr := exec.Command(aapt2, "convert", "--output-format", "binary",
					"-o", resAP, shrunk).CombinedOutput()
				if cerr != nil {
					fmt.Printf("警告: 收缩资源转换失败，使用未收缩资源: %s\n", string(cout))
				} else {
					if si, serr := os.Stat(shrunk); serr == nil {
						fmt.Printf("资源收缩: resources.ap_ 由 R8 收缩产物转换 (%dKB)\n", si.Size()/1024)
					}
				}
			}
		}
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

		// 3d. 依赖 jar 的数据资源（META-INF/*.version、services、kotlin_builtins 等），
		//     应用 packaging.resources.excludes（对齐 AGP packaging 流程）
		var excludes []string
		if cfg != nil {
			excludes = cfg.ResourceExcludes
		}
		dataEntries := dataJarEntries(filepath.Join(ctx.BuildDir, "merged_data.jar"), excludes)
		for name, data := range dataEntries {
			dst := filepath.Join(tmpDir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err == nil {
				os.WriteFile(dst, data, 0644)
			}
		}
		if len(dataEntries) > 0 {
			fmt.Printf("数据资源: %d 个条目入包（excludes %d 条）\n", len(dataEntries), len(excludes))
		}

		// 4. 打包 APK（universal + per-ABI）
		for _, v := range apkVariants(cfg) {
			if err := writeApk(tmpDir, filepath.Join(ctx.BuildDir, v.Name+".apk"), v.ABI, cfg != nil && cfg.UseLegacyPackaging); err != nil {
				fmt.Printf("打包 APK 失败: %v\n", err)
				return false
			}
			if info, err := os.Stat(filepath.Join(ctx.BuildDir, v.Name+".apk")); err == nil {
				label := v.Name
				if v.ABI == "" {
					label = "universal"
				}
				fmt.Printf("APK [%s]: %s (%dKB)\n", label, v.Name+".apk", info.Size()/1024)
			}
		}
		return true
	}
	return t
}

// writeApk 将 tmpDir 内容打包为 APK；abi 非空时仅打包该 ABI 的 native 库
func writeApk(tmpDir, apkPath, abi string, legacyPackaging bool) error {
	src := tmpDir
	if abi != "" {
		// per-ABI：在独立临时目录重排，仅保留该 ABI 的 lib/
		filtered, err := os.MkdirTemp("", "canter-apk-abi")
		if err != nil {
			return err
		}
		defer os.RemoveAll(filtered)
		if err := copyTree(src, filtered); err != nil {
			return err
		}
		libDir := filepath.Join(filtered, "lib")
		if entries, err := os.ReadDir(libDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && e.Name() != abi {
					os.RemoveAll(filepath.Join(libDir, e.Name()))
				}
			}
		}
		src = filtered
	}

	out, err := os.Create(apkPath)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		relPath, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return nil
		}
		rel := filepath.ToSlash(relPath)
		// native 库打包策略：默认不压缩（对齐 AGP，便于运行时 mmap）；
		// useLegacyPackaging=true 时压缩（安装时解压到文件系统，AGP release 常用）
		if strings.HasPrefix(rel, "lib/") && strings.HasSuffix(rel, ".so") && !legacyPackaging {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: rel, Method: zip.Store})
			if err != nil {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			w.Write(data)
			return nil
		}
		w, err := zw.Create(rel)
		if err != nil {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		w.Write(data)
		return nil
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// keystoreRef 一次签名所用的密钥信息
type keystoreRef struct {
	Path          string
	StorePassword string
	KeyAlias      string
	KeyPassword   string
	IsDebug       bool
}

// resolveKeystore 解析签名配置：release 时优先 signingConfigs（含 env: 引用
// 与 WearPomodoro 风格的 ANDROID_KEYSTORE_* 环境变量），否则回退 debug keystore
func resolveKeystore(cfg *AppConfig, projectDir string) keystoreRef {
	home, _ := os.UserHomeDir()
	debug := keystoreRef{
		Path:          filepath.Join(home, ".android", "debug.keystore"),
		StorePassword: "android",
		KeyPassword:   "android",
		IsDebug:       true,
	}
	if cfg == nil || !cfg.Release {
		return debug
	}
	// 项目自定义约定：ANDROID_KEYSTORE_FILE/PASSWORD/ALIAS/KEY_PASSWORD 环境变量
	if ks := os.Getenv("ANDROID_KEYSTORE_FILE"); ks != "" {
		ref := keystoreRef{
			Path:          ks,
			StorePassword: os.Getenv("ANDROID_KEYSTORE_PASSWORD"),
			KeyAlias:      os.Getenv("ANDROID_KEY_ALIAS"),
			KeyPassword:   os.Getenv("ANDROID_KEY_PASSWORD"),
		}
		if fi, err := os.Stat(ref.Path); err == nil && !fi.IsDir() &&
			ref.StorePassword != "" && ref.KeyAlias != "" {
			return ref
		}
		fmt.Println("警告: ANDROID_KEYSTORE_* 环境变量不完整，回退 debug 证书")
		return debug
	}
	// signingConfigs { create("release") {...} } 字面量/env 引用
	name := cfg.SigningConfig
	if name == "" {
		name = "release"
	}
	sc, ok := cfg.SigningConfigs[name]
	if !ok {
		return debug
	}
	ref := keystoreRef{
		Path:          resolveSigningValue(sc.StoreFile, projectDir),
		StorePassword: resolveSigningValue(sc.StorePassword, projectDir),
		KeyAlias:      resolveSigningValue(sc.KeyAlias, projectDir),
		KeyPassword:   resolveSigningValue(sc.KeyPassword, projectDir),
	}
	if ref.Path != "" && ref.StorePassword != "" && ref.KeyAlias != "" {
		if fi, err := os.Stat(ref.Path); err == nil && !fi.IsDir() {
			return ref
		}
	}
	fmt.Printf("警告: signingConfig %q 不完整或 keystore 缺失，回退 debug 证书\n", name)
	return debug
}

// resolveSigningValue 解析 parser 产出的原始表达式：
// "file:x"（相对项目根）、"env:X"、字面量
func resolveSigningValue(v, projectDir string) string {
	switch {
	case strings.HasPrefix(v, "file:"):
		rel := strings.TrimPrefix(v, "file:")
		if filepath.IsAbs(rel) {
			return rel
		}
		return filepath.Join(projectDir, rel)
	case strings.HasPrefix(v, "env:"):
		return os.Getenv(strings.TrimPrefix(v, "env:"))
	}
	return v
}

// SignApkTask 签名 APK（universal + per-ABI 全部签名）
func SignApkTask(ctx *engine.BuildContext) *engine.Task {
	cfg, _ := ctx.Config.(*AppConfig)
	variants := apkVariants(cfg)
	t := engine.NewTask("signApk")
	for _, v := range variants {
		t.AddFileInputs(filepath.Join(ctx.BuildDir, v.Name+".apk"))
		t.AddFileOutputs(filepath.Join(ctx.BuildDir, v.Name+"-signed.apk"))
	}
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		apksigner := filepath.Join(ctx.BuildTools, "apksigner")
		ks := resolveKeystore(ctx.Config.(*AppConfig), ctx.ProjectDir)
		if ks.IsDebug {
			if _, err := os.Stat(ks.Path); err != nil {
				fmt.Println("警告: debug.keystore 未找到，跳过签名")
				for _, v := range variants {
					copyFile(filepath.Join(ctx.BuildDir, v.Name+".apk"),
						filepath.Join(ctx.BuildDir, v.Name+"-signed.apk"))
				}
				return true
			}
		} else {
			fmt.Printf("release 签名: %s (alias=%s)\n", ks.Path, ks.KeyAlias)
		}
		if _, err := os.Stat(apksigner); err != nil {
			fmt.Println("警告: apksigner 未找到，跳过签名")
			for _, v := range variants {
				copyFile(filepath.Join(ctx.BuildDir, v.Name+".apk"),
					filepath.Join(ctx.BuildDir, v.Name+"-signed.apk"))
			}
			return true
		}

		for _, v := range variants {
			unsigned := filepath.Join(ctx.BuildDir, v.Name+".apk")
			signed := filepath.Join(ctx.BuildDir, v.Name+"-signed.apk")
			// 先对齐再签名（AGP 流程）。未压缩的 .so 需 4KiB 页对齐才能被直接 mmap
			input := zipAlignApk(ctx, unsigned)

			args := []string{
				"sign",
				"--ks", ks.Path,
				"--ks-pass", "pass:" + ks.StorePassword,
				"--key-pass", "pass:" + ks.KeyPassword,
				"--out", signed,
				input,
			}
			if ks.KeyAlias != "" {
				args = append(args[:3], append([]string{"--ks-key-alias", ks.KeyAlias}, args[3:]...)...)
			}
			cmd := exec.Command(apksigner, args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Printf("签名失败 %s: %s\n", v.Name, string(out))
				return false
			}
		}
		return true
	}
	return t
}

// zipAlignApk 用 zipalign 对 APK 做 4 字节 + 未压缩 .so 页对齐（对齐 AGP）。
// 返回对齐后的 APK 路径；zipalign 缺失或失败时返回原路径。
func zipAlignApk(ctx *engine.BuildContext, apk string) string {
	zipalign := filepath.Join(ctx.BuildTools, "zipalign")
	if _, err := os.Stat(zipalign); err != nil {
		fmt.Println("警告: zipalign 未找到，跳过对齐")
		return apk
	}
	aligned := strings.TrimSuffix(apk, ".apk") + "-aligned.apk"
	out, err := exec.Command(zipalign, "-f", "-p", "4", apk, aligned).CombinedOutput()
	if err != nil {
		fmt.Printf("警告: zipalign 失败，使用未对齐 APK: %s\n", strings.TrimSpace(string(out)))
		return apk
	}
	return aligned
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
