package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"canter/internal/mirror"
)

func cmdMirror(args []string) {
	if len(args) < 1 {
		fmt.Println("用法: canter mirror <list|select|combo|speedtest|auto|generate-settings|wrapper-props|suggest>")
		osExit(1)
	}
	action := args[0]
	rest := args[1:]
	mgr := mirror.NewManager("")

	switch action {
	case "list":
		showStatus(mgr)
	case "select":
		fs := flag.NewFlagSet("select", flag.ExitOnError)
		category := fs.String("category", "maven", "maven|google|plugins|gradle_dist")
		fs.Parse(rest)
		if fs.NArg() < 1 {
			fmt.Println("用法: canter mirror select <name> [--category <type>]")
			osExit(1)
		}
		name := fs.Arg(0)
		if mgr.SelectByName(name, *category) {
			fmt.Printf("已选择: %s\n", name)
		} else {
			fmt.Printf("未找到: %s\n", name)
			showStatus(mgr)
		}
	case "combo":
		fs := flag.NewFlagSet("combo", flag.ExitOnError)
		fs.Parse(rest)
		if fs.NArg() < 1 {
			fmt.Println("用法: canter mirror combo <combo_id>")
			osExit(1)
		}
		if combo, ok := mgr.SelectCombo(fs.Arg(0)); ok {
			fmt.Printf("已选择组合: %s\n", combo.Name)
			fmt.Printf("  %s\n", combo.Description)
		} else {
			fmt.Println("未找到组合")
		}
	case "speedtest":
		fs := flag.NewFlagSet("speedtest", flag.ExitOnError)
		category := fs.String("category", "maven", "all|maven|google|plugins|gradle_dist|sdk")
		fs.Parse(rest)
		fmt.Println("\n=== 测速结果 ===")
		for _, r := range mgr.SpeedTest(*category, 0) {
			fmt.Printf("  %s\n", r.String())
		}
	case "auto":
		fs := flag.NewFlagSet("auto", flag.ExitOnError)
		category := fs.String("category", "maven", "all|maven|google|plugins|gradle_dist|sdk")
		fs.Parse(rest)
		if best := mgr.AutoSelect(*category); best != nil {
			fmt.Printf("已自动选择: %s\n", best.Name)
		}
	case "generate-settings":
		fmt.Println(mgr.GenerateSettingsKts())
	case "wrapper-props":
		fs := flag.NewFlagSet("wrapper-props", flag.ExitOnError)
		version := fs.String("gradle-version", "8.10.2", "gradle version")
		fs.Parse(rest)
		fmt.Println(mgr.GenerateWrapperProperties(*version))
	case "suggest":
		suggest(mgr)
	default:
		fmt.Printf("未知镜像命令: %s\n", action)
		osExit(1)
	}
}

func osExit(code int) {
	os.Exit(code)
}

func showStatus(mgr *mirror.Manager) {
	sep := strings.Repeat("=", 70)
	fmt.Println()
	fmt.Println(sep)
	fmt.Println("  镜像源配置")
	fmt.Println(sep)

	printCategory := func(label string, mirrors []mirror.Mirror) {
		fmt.Printf("\n%s:\n", label)
		for _, m := range mirrors {
			marker := "  "
			if mgr.IsSelected(m) {
				marker = " ★"
			}
			fmt.Printf("%s [%-20s] %s\n", marker, m.Name, m.URL)
		}
	}

	printCategory("Maven Central", mgr.ListMaven())
	printCategory("Google Maven", mgr.ListGoogle())
	printCategory("插件门户", mgr.ListPlugins())
	printCategory("Gradle 发行版", mgr.ListGradleDist())
	printCategory("Android SDK", mgr.ListSDK())
	printCategory("JDK", mgr.ListJDK())

	fmt.Println("\n推荐组合:")
	for _, c := range mirror.RecommendedCombos {
		marker := "  "
		if mgr.IsComboSelected(c.ID) {
			marker = " ★"
		}
		fmt.Printf("%s [%-20s] %s\n", marker, c.ID, c.Name)
		fmt.Printf("      %s\n", c.Description)
	}
	fmt.Printf("\nGitHub 代理: %s\n", mirror.GitHubProxy)
	fmt.Printf("Android SDK 镜像: %v\n", []string{"https://mirrors.cloud.tencent.com/AndroidSDK"})
	fmt.Println(sep)
}

func suggest(mgr *mirror.Manager) {
	fmt.Println("\n推荐组合:")
	fmt.Println(strings.Repeat("-", 60))
	fmt.Println()
	for _, c := range mirror.RecommendedCombos {
		marker := ""
		if mgr.IsComboSelected(c.ID) {
			marker = " ✓ 已选"
		}
		fmt.Printf("  [%s]%s\n", c.ID, marker)
		fmt.Printf("    名称: %s\n", c.Name)
		fmt.Printf("    描述: %s\n", c.Description)
	}
	fmt.Println("\n使用: canter mirror combo <组合id>")
	fmt.Println()
}
