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
		compilerJar := findKotlinCompilerJar()
		if compilerJar == "" {
			fmt.Println("警告: 未找到 kotlin-compiler-embeddable.jar，跳过 Kotlin 编译")
			return false
		}

		out := filepath.Join(ctx.BuildDir, "kotlin_classes")
		os.MkdirAll(out, 0755)

		// 运行时 classpath：compiler + 其依赖的 stdlib/reflect/script-runtime 等 jar
		compilerCp := collectKotlinRuntimeJars(compilerJar)
		classpath := buildClasspath(ctx)
		classpath = withJavaClassesOutput(classpath, ctx)
		classpath = withAndroidJar(classpath, ctx)
		classpath += string(os.PathListSeparator) + filepath.Join(ctx.BuildDir, "classes")

		parcelize := false
		if ac, ok := ctx.Config.(*AppConfig); ok && ac.Parcelize {
			parcelize = true
			// parcelize 注解库在编译 classpath 上
			if rt := findParcelizeRuntimeJar(); rt != "" {
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
			if pj := findParcelizeCompilerJar(); pj != "" {
				args = append(args, "-Xplugin="+pj)
			}
		}
		// Compose 编译器插件
		if ac, ok := ctx.Config.(*AppConfig); ok && ac.Compose {
			if cj := findComposeCompilerJar(); cj != "" {
				args = append(args, "-Xplugin="+cj)
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

// findComposeCompilerJar 从 Gradle 缓存中找 Compose 编译器插件（embeddable）
func findComposeCompilerJar() string {
	home, _ := os.UserHomeDir()
	gradleCache := filepath.Join(home, ".gradle", "caches")
	var found string
	filepath.Walk(gradleCache, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, "kotlin-compose-compiler-plugin-embeddable-") && strings.HasSuffix(name, ".jar") &&
			!strings.Contains(name, "sources") && !strings.Contains(name, "javadoc") {
			if found == "" || name > found {
				found = path
			}
		}
		return nil
	})
	return found
}

// findParcelizeRuntimeJar 从 Gradle 缓存中找 kotlin-parcelize-runtime 注解库
func findParcelizeRuntimeJar() string {
	home, _ := os.UserHomeDir()
	gradleCache := filepath.Join(home, ".gradle", "caches")
	var found string
	filepath.Walk(gradleCache, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, "kotlin-parcelize-runtime-") && strings.HasSuffix(name, ".jar") &&
			!strings.Contains(name, "sources") && !strings.Contains(name, "javadoc") {
			if found == "" || name > found {
				found = path
			}
		}
		return nil
	})
	return found
}

// findParcelizeCompilerJar 从 Gradle 缓存中找 kotlin-parcelize-compiler 插件 jar
func findParcelizeCompilerJar() string {
	home, _ := os.UserHomeDir()
	gradleCache := filepath.Join(home, ".gradle", "caches")
	var found string
	filepath.Walk(gradleCache, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, "kotlin-parcelize-compiler-") && strings.HasSuffix(name, ".jar") &&
			!strings.Contains(name, "sources") && !strings.Contains(name, "javadoc") {
			if found == "" || name > found {
				found = path
			}
		}
		return nil
	})
	return found
}

// findKotlinCompilerJar 从 Gradle 缓存中找 kotlin-compiler-embeddable jar
func findKotlinCompilerJar() string {
	home, _ := os.UserHomeDir()
	gradleCache := filepath.Join(home, ".gradle", "caches")
	var found string
	filepath.Walk(gradleCache, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if found != "" {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasPrefix(info.Name(), "kotlin-compiler-embeddable-") &&
			strings.HasSuffix(info.Name(), ".jar") && !strings.Contains(info.Name(), "sources") &&
			!strings.Contains(info.Name(), "javadoc") {
			found = path
			return filepath.SkipDir
		}
		return nil
	})
	return found
}

// collectKotlinRuntimeJars 收集运行 K2JVMCompiler 所需的所有依赖 jar（stdlib/reflect/script-runtime 等）
func collectKotlinRuntimeJars(compilerJar string) string {
	home, _ := os.UserHomeDir()
	gradleCache := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1")
	saw := map[string]string{} // name -> path
	knownPrefixes := []string{
		"kotlin-stdlib-", "kotlin-reflect-", "kotlin-script-runtime-",
		"kotlin-scripting-common-", "kotlin-scripting-jvm-",
		"kotlin-scripting-compiler-embeddable-", "kotlin-scripting-compiler-impl-embeddable-",
		"trove4j-", "kotlinx-coroutines-core-", "annotations-",
	}
	filepath.Walk(gradleCache, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".jar") || strings.Contains(name, "sources") || strings.Contains(name, "javadoc") {
			return nil
		}
		for _, p := range knownPrefixes {
			if strings.HasPrefix(name, p) {
				// 有版本差异时取最新（按名字排序取最高的）
				existing, ok := saw[name]
				if !ok || name > existing {
					saw[name] = path
				}
				break
			}
		}
		return nil
	})

	var jars []string
	jars = append(jars, compilerJar)
	keys := make([]string, 0, len(saw))
	for k := range saw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		jars = append(jars, saw[k])
	}
	if len(jars) <= 1 {
		return compilerJar
	}
	return strings.Join(jars, string(os.PathListSeparator))
}

// findKotlinStdlibJar 从 Gradle 缓存中找核心 kotlin-stdlib jar（排除 jdk7/jdk8/common，优先 2.x 最新）
func findKotlinStdlibJar() string {
	home, _ := os.UserHomeDir()
	gradleCache := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1")
	var latest string
	filepath.Walk(gradleCache, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, "kotlin-stdlib-") && strings.HasSuffix(name, ".jar") &&
			!strings.Contains(name, "sources") && !strings.Contains(name, "javadoc") &&
			!strings.Contains(name, "-jdk7") && !strings.Contains(name, "-jdk8") &&
			!strings.HasPrefix(name, "kotlin-stdlib-common") {
			if latest == "" || name > latest {
				latest = path
			}
		}
		return nil
	})
	return latest
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