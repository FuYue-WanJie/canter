package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"canter/internal/engine"
	"canter/internal/parser"
)

// CompileProjectLibraries 编译 project 依赖的 library 模块（多模块支持）
// 每个库模块 javac+kotlinc 编译到 build/libs/<name>/classes，
// 之后 buildClasspath 会把这些目录并入 app 的编译 classpath。
func CompileProjectLibraries(ctx *engine.BuildContext, config *parser.ProjectConfig) []string {
	var libClasses []string
	for _, mod := range config.Modules {
		if mod.Path == ctx.ModuleDirOrProject() {
			continue // 跳过 app 模块自身
		}
		classDir := compileLibraryModule(ctx, mod)
		if classDir != "" {
			libClasses = append(libClasses, classDir)
		}
	}
	return libClasses
}

// compileLibraryModule 编译单个 library 模块，返回 classes 目录（失败返回空）
func compileLibraryModule(ctx *engine.BuildContext, mod parser.ModuleConfig) string {
	roots := moduleSourceRootsCtx(mod.Path, ctx)
	javaSources, hasJava := collectSourcesInRoots(roots, ".java")
	ktSources, hasKotlin := collectSourcesInRoots(roots, ".kt")
	if !hasJava && !hasKotlin {
		return ""
	}
	baseOut := filepath.Join(ctx.BuildDir, "libs", mod.Name, "classes")
	os.MkdirAll(baseOut, 0755)

	// 增量缓存：源码及 classpath 未变则跳过编译
	sig := librarySourceSignature(javaSources, ktSources, ctx.AndroidJar, ctx.KotlinVersion)
	sigFile := filepath.Join(baseOut, ".canter-sig")
	if data, err := os.ReadFile(sigFile); err == nil && string(data) == sig && dirHasClass(baseOut) {
		fmt.Printf("库模块 %s: 缓存命中，跳过编译\n", mod.Name)
		return baseOut
	}

	// 库模块的 classpath：android.jar + kotlin-stdlib
	tc := ToolchainFor(ctx)
	cp := ctx.AndroidJar
	if stdlib := tc.KotlinStdlibJar(); stdlib != "" {
		cp += string(os.PathListSeparator) + stdlib
	}

	// javac 编译 Java 源码
	if hasJava {
		javac := "javac"
		if ctx.JavaHome != "" {
			javac = filepath.Join(ctx.JavaHome, "bin", "javac")
		}
		args := []string{"-cp", cp, "-d", baseOut}
		args = append(args, javaSources...)
		cmd := exec.Command(javac, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("库模块 %s javac 失败: %s\n", mod.Name, strings.TrimSpace(string(out)))
			return ""
		}
	}

	// kotlinc 编译 Kotlin 源码
	if hasKotlin {
		compilerJar := tc.KotlinCompilerJar()
		if compilerJar == "" {
			fmt.Printf("库模块 %s：未找到 kotlin 编译器，跳过\n", mod.Name)
			return ""
		}
		compilerCp := tc.CompilerClasspath(compilerJar)
		javaBin := "java"
		if ctx.JavaHome != "" {
			javaBin = filepath.Join(ctx.JavaHome, "bin", "java")
		}
		args := []string{
			"-cp", compilerCp, "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler",
			"-no-stdlib",
			"-classpath", cp,
			"-d", baseOut,
		}
		args = append(args, ktSources...)
		cmd := exec.Command(javaBin, append([]string{jvmXmxFlag()}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("库模块 %s kotlinc 失败: %s\n", mod.Name, strings.TrimSpace(string(out)))
			return ""
		}
	}

	fmt.Printf("库模块编译完成: %s -> %s\n", mod.Name, baseOut)
	os.WriteFile(sigFile, []byte(sig), 0644)
	return baseOut
}

// librarySourceSignature 库模块源码 + classpath 的签名
func librarySourceSignature(javaSources, ktSources []string, androidJar, kotlinVersion string) string {
	h := sha256.New()
	all := append(append([]string{}, javaSources...), ktSources...)
	sort.Strings(all)
	for _, p := range all {
		h.Write([]byte(p))
		if fi, err := os.Stat(p); err == nil {
			h.Write([]byte(strconv.FormatInt(fi.Size(), 10)))
			h.Write([]byte(strconv.FormatInt(fi.ModTime().UnixNano(), 10)))
		}
	}
	h.Write([]byte("#" + androidJar))
	h.Write([]byte("#kotlin=" + kotlinVersion))
	return hex.EncodeToString(h.Sum(nil))
}

// dirHasClass 判断目录下是否存在 .class 文件
func dirHasClass(dir string) bool {
	found := false
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".class") {
			found = true
		}
		return nil
	})
	return found
}

// projectLibClassDirs 返回 build/libs 下已编译的库类目录
func projectLibClassDirs(ctx *engine.BuildContext) []string {
	libsRoot := filepath.Join(ctx.BuildDir, "libs")
	var dirs []string
	entries, err := os.ReadDir(libsRoot)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			classDir := filepath.Join(libsRoot, e.Name(), "classes")
			if fi, err := os.Stat(classDir); err == nil && fi.IsDir() {
				dirs = append(dirs, classDir)
			}
		}
	}
	return dirs
}