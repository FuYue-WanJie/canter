package builder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"canter/internal/engine"
)

// JavaCompileTask 用 javac 编译 Java 源码 + 生成的 R.java/BuildConfig.java
func JavaCompileTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("compileJava")
	module := ctx.ModuleDirOrProject()
	t.AddDirInputs(filepath.Join(module, "src", "main", "java"))
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "gen"))
	t.AddFileInputs(depsSignatureFile(ctx))
	t.AddDirOutputs(filepath.Join(ctx.BuildDir, "classes"))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		javac := filepath.Join(ctx.JavaHome, "bin", "javac")
		if _, err := os.Stat(javac); err != nil {
			javac = "javac"
		}
		if _, err := exec.LookPath(javac); err != nil {
			fmt.Printf("javac 未找到\n")
			return false
		}

		var sources []string
		module := ctx.ModuleDirOrProject()
		roots := moduleSourceRootsCtx(module, ctx)
		javaFiles, _ := collectSourcesInRoots(roots, ".java")
		sources = append(sources, javaFiles...)
		hasJava := len(javaFiles) > 0
		// 生成的 R.java/BuildConfig.java
		genDir := filepath.Join(ctx.BuildDir, "gen")
		filepath.Walk(genDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".java") {
				sources = append(sources, path)
				hasJava = true
			}
			return nil
		})
		if !hasJava {
			return true
		}

		sort.Strings(sources)
		out := filepath.Join(ctx.BuildDir, "classes")
		os.MkdirAll(out, 0755)

		classpath := buildClasspath(ctx)
		classpath = withAndroidJar(classpath, ctx)
		jvmTarget := getJvmTarget(ctx.Config)
		args := []string{
			"-cp", classpath,
			"-d", out,
		}
		if jvmTarget != "" {
			args = append(args, "--release", jvmTarget)
		} else {
			args = append(args, "-source", "17", "-target", "17")
		}
		args = append(args, sources...)
		cmd := exec.Command(javac, args...)
		cmdOut, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("javac 编译失败: %s\n", strings.TrimSpace(string(cmdOut)))
			return false
		}
		fmt.Printf("javac 编译 %d 个源文件\n", len(sources))
		return true
	}
	return t
}

// KotlinCompileTask 用 kotlin-compiler-embeddable 编译 Kotlin 源码
func KotlinCompileTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("compileKotlin")
	module := ctx.ModuleDirOrProject()
	t.AddDirInputs(filepath.Join(module, "src", "main", "java"))
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "classes"))
	t.AddFileInputs(depsSignatureFile(ctx))
	t.AddDirOutputs(filepath.Join(ctx.BuildDir, "kotlin_classes"))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		module := ctx.ModuleDirOrProject()
		ktSources, hasKotlin := collectSourcesInRoots(moduleSourceRootsCtx(module, ctx), ".kt")
		if !hasKotlin {
			return true
		}
		sort.Strings(ktSources)

		javaBin := "java"
		if ctx.JavaHome != "" {
			javaBin = filepath.Join(ctx.JavaHome, "bin", "java")
		}
		tc := ToolchainFor(ctx)
		compilerJar := tc.KotlinCompilerJar()
		if compilerJar == "" {
			fmt.Println("警告: 未找到 kotlin-compiler-embeddable.jar，跳过 Kotlin 编译")
			return false
		}

		out := filepath.Join(ctx.BuildDir, "kotlin_classes")
		os.MkdirAll(out, 0755)

		// 运行时 classpath：compiler + 其依赖的 stdlib/reflect/script-runtime 等 jar
		compilerCp := tc.CompilerClasspath(compilerJar)
		classpath := buildClasspath(ctx)
		classpath = withJavaClassesOutput(classpath, ctx)
		classpath = withAndroidJar(classpath, ctx)
		classpath += string(os.PathListSeparator) + filepath.Join(ctx.BuildDir, "classes")

		parcelize := false
		if ac, ok := ctx.Config.(*AppConfig); ok && ac.Parcelize {
			parcelize = true
			// parcelize 注解库在编译 classpath 上
			if rt := tc.ParcelizeRuntimeJar(); rt != "" {
				classpath += string(os.PathListSeparator) + rt
			}
		}

		args := []string{
			"-cp", compilerCp, "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler",
			"-no-stdlib",
			"-classpath", classpath,
			"-d", out,
		}
		if jvmTarget := getJvmTarget(ctx.Config); jvmTarget != "" {
			args = append(args, "-jvm-target", jvmTarget)
		}
		// kotlin-parcelize 编译器插件
		if parcelize {
			if pj := tc.ParcelizeCompilerJar(); pj != "" {
				args = append(args, "-Xplugin="+pj)
			}
		}
		// Compose 编译器插件
		if ac, ok := ctx.Config.(*AppConfig); ok && ac.Compose {
			if cj := tc.ComposeCompilerJar(); cj != "" {
				args = append(args, "-Xplugin="+cj)
			}
		}
		// kotlin-serialization 编译器插件
		if ac, ok := ctx.Config.(*AppConfig); ok && ac.Serialization {
			if sj := tc.SerializationCompilerJar(); sj != "" {
				args = append(args, "-Xplugin="+sj)
			}
		}
		args = append(args, ktSources...)
		cmd := exec.Command(javaBin, args...)
		cmdOut, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("Kotlin 编译失败: %s\n", strings.TrimSpace(string(cmdOut)))
			return false
		}
		fmt.Printf("Kotlin 编译 %d 个源文件 (jar: %s)\n", len(ktSources), filepath.Base(compilerJar))
		return true
	}
	return t
}

// getJvmTarget 从 AppConfig 提取 JVM 编译目标（如 "17"），为空时回退 "17"
func getJvmTarget(cfg interface{}) string {
	if ac, ok := cfg.(*AppConfig); ok && ac.JvmTarget != "" {
		// 解析 "17" 这类数字；若是 "1.8" 保留原样
		return strings.TrimSpace(ac.JvmTarget)
	}
	return "17"
}

// withAndroidJar 确保 classpath 含 android.jar
func withAndroidJar(cp string, ctx *engine.BuildContext) string {
	if _, err := os.Stat(ctx.AndroidJar); err != nil {
		return cp
	}
	if cp == "" {
		return ctx.AndroidJar
	}
	return cp + string(os.PathListSeparator) + ctx.AndroidJar
}

// withJavaClassesOutput 确保 classpath 含已编译的 Java 类目录
func withJavaClassesOutput(cp string, ctx *engine.BuildContext) string {
	classes := filepath.Join(ctx.BuildDir, "classes")
	if _, err := os.Stat(classes); err != nil {
		return cp
	}
	if cp == "" {
		return classes
	}
	return cp + string(os.PathListSeparator) + classes
}