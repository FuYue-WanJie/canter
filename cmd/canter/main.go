package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"canter/internal/builder"
	"canter/internal/checker"
	"canter/internal/mirror"
	"canter/internal/parser"
)

const usage = `Canter - 轻量级 Android 构建工具

用法:
  canter assemble [project] [-v] [--release] [--flavor F]      构建项目（--release 启用 R8；--flavor 选 productFlavor）
  canter clean [project]                           清理构建产物
  canter check [project]                           检查工具链
  canter mirror <action> [options]                 管理镜像源
  canter parse [project]                           解析并显示 Gradle 配置
  canter deps [project]                            下载依赖
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}
	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "assemble":
		cmdAssemble(args)
	case "clean":
		cmdClean(args)
	case "check":
		cmdCheck(args)
	case "mirror":
		cmdMirror(args)
	case "parse":
		cmdParse(args)
	case "deps":
		cmdDeps(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Printf("未知命令: %s\n\n", command)
		fmt.Print(usage)
		os.Exit(1)
	}
}

// normalizeArgs 把选项（"-xxx"）移到位置参数之前。
// Go 的 flag 包遇到首个非选项参数即停止解析，而本工具的用法是
// `canter assemble <project> --release`，故需先重排。
func normalizeArgs(args []string) []string {
	return normalizeArgsWithValue(args, "")
}

// normalizeArgsWithValue 同 normalizeArgs，且把带值选项的值随选项一起前置
// （如 --flavor full 的 "full" 不能落在位置参数段）
func normalizeArgsWithValue(args []string, valuedFlag string) []string {
	var flags, positional []string
	i := 0
	for i < len(args) {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// -flavor full / --flavor=full：后者值已含在 token 内
			name := strings.TrimLeft(a, "-")
			if eq := strings.Index(name, "="); eq >= 0 {
				i++
				continue
			}
			if name == valuedFlag && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i += 2
				continue
			}
			i++
			continue
		}
		positional = append(positional, a)
		i++
	}
	return append(flags, positional...)
}

func resolveProject(args []string, fs *flag.FlagSet) string {
	project := "."
	le := fs.NArg()
	if le > 0 {
		project = fs.Arg(0)
	}
	abs, err := filepath.Abs(project)
	if err != nil {
		return project
	}
	return abs
}

func cmdAssemble(args []string) {
	fs := flag.NewFlagSet("assemble", flag.ExitOnError)
	verbose := fs.Bool("v", false, "verbose output")
	checkFirst := fs.Bool("check-first", false, "check toolchain first")
	release := fs.Bool("release", false, "build release variant (R8 when minifyEnabled)")
	flavor := fs.String("flavor", "", "product flavor to build (default: first declared)")
	// 选项值（如 --flavor full 的 "full"）需与 flag 一起前置，避免被当作位置参数
	fs.Parse(normalizeArgsWithValue(args, "flavor"))
	_ = verbose

	projectDir := resolveProject(args, fs)
	config := parser.GradleConfigParser{}.Parse(projectDir)

	if *checkFirst {
		runCheck(projectDir, config)
	}

	b := builder.NewBuilder(projectDir, config)
	if err := b.Assemble(*release, *flavor); err != nil {
		fmt.Fprintf(os.Stderr, "构建失败: %v\n", err)
		os.Exit(1)
	}
}

func cmdClean(args []string) {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	fs.Parse(normalizeArgs(args))
	projectDir := resolveProject(args, fs)
	config := parser.GradleConfigParser{}.Parse(projectDir)
	b := builder.NewBuilder(projectDir, config)
	b.Clean()
}

func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	fs.Parse(normalizeArgs(args))
	projectDir := resolveProject(args, fs)
	config := parser.GradleConfigParser{}.Parse(projectDir)
	runCheck(projectDir, config)
}

func runCheck(projectDir string, config *parser.ProjectConfig) {
	ch := checker.NewToolchainChecker("")
	ch.CheckAll(config)
	ok := ch.ShowReport()
	if !ok {
		fmt.Println("建议执行的修复命令:")
		for _, cmd := range ch.SuggestFixCommands() {
			fmt.Printf("  %s\n", cmd)
		}
	}
}

func cmdParse(args []string) {
	fs := flag.NewFlagSet("parse", flag.ExitOnError)
	fs.Parse(normalizeArgs(args))
	projectDir := resolveProject(args, fs)
	config := parser.GradleConfigParser{}.Parse(projectDir)

	fmt.Printf("项目名称: %s\n", config.ProjectName)
	fmt.Printf("根目录: %s\n", config.RootDir)
	fmt.Println("模块:")
	for _, mod := range config.Modules {
		fmt.Printf("  - %s (%s)\n", mod.Name, mod.Path)
		if mod.Android != nil {
			a := mod.Android
			fmt.Printf("    namespace: %s\n", a.Namespace)
			if a.CompileSDK != nil {
				fmt.Printf("    compileSdk: %g\n", *a.CompileSDK)
			}
			if a.MinSDK != nil {
				fmt.Printf("    minSdk: %d\n", *a.MinSDK)
			}
			if a.TargetSDK != nil {
				fmt.Printf("    targetSdk: %d\n", *a.TargetSDK)
			}
			if a.VersionCode != nil {
				fmt.Printf("    versionCode: %d\n", *a.VersionCode)
			}
			fmt.Printf("    applicationId: %s\n", a.ApplicationID)
			fmt.Printf("    minifyEnabled: %v, shrinkResources: %v, signingConfig: %q\n",
				a.MinifyEnabled, a.ShrinkResources, a.SigningConfig)
			if len(a.ProguardFiles) > 0 {
				fmt.Printf("    proguardFiles: %v\n", a.ProguardFiles)
			}
		}
		if len(mod.Dependencies) > 0 {
			fmt.Printf("    依赖 (%d):\n", len(mod.Dependencies))
			for _, dep := range mod.Dependencies {
				if dep.IsProject {
					fmt.Printf("      project: %s\n", dep.ProjectPath)
				} else if dep.IsPlatform {
					fmt.Printf("      platform: %s:%s:%s\n", dep.Group, dep.Artifact, dep.Version)
				} else {
					fmt.Printf("      %s: %s:%s:%s\n", dep.Scope, dep.Group, dep.Artifact, dep.Version)
				}
			}
		}
	}
	fmt.Printf("仓库: %v\n", config.Repositories)
	fmt.Printf("插件仓库: %v\n", config.PluginRepositories)
}

func cmdDeps(args []string) {
	fs := flag.NewFlagSet("deps", flag.ExitOnError)
	fs.Parse(normalizeArgs(args))
	projectDir := resolveProject(args, fs)
	config := parser.GradleConfigParser{}.Parse(projectDir)

	home, _ := os.UserHomeDir()
	mgr := mirror.NewManager("")
	repos := mgr.GetRepositories()
	downloader := builder.NewDownloader(repos, filepath.Join(home, ".canter", "cache", "deps"))

	var allDeps []parser.Dependency
	for _, mod := range config.Modules {
		for _, dep := range mod.Dependencies {
			if !dep.IsProject && dep.Group != "" {
				allDeps = append(allDeps, dep)
			}
		}
	}
	fmt.Printf("共 %d 个依赖\n", len(allDeps))

	success, failed := 0, 0
	for _, dep := range allDeps {
		if dep.Version == "" || dep.Version == "unknown" {
			fmt.Printf("跳过（版本未知）: %s:%s\n", dep.Group, dep.Artifact)
			failed++
			continue
		}
		if _, err := downloader.Download(dep.Group, dep.Artifact, dep.Version); err != nil {
			fmt.Printf("下载失败: %s:%s:%s: %v\n", dep.Group, dep.Artifact, dep.Version, err)
			failed++
		} else {
			success++
		}
	}
	fmt.Printf("下载完成: 成功 %d, 失败 %d\n", success, failed)
}
