package builder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"canter/internal/engine"
)

// BuildConfigGenerated 记录 BuildConfig 已生成的标记（保持可重入）
const buildConfigMarker = ".buildconfig.generated"

// SourceGenTask 生成 BuildConfig.java（对齐 AGP：APPLICATION_ID/DEBUG/BUILD_TYPE/FLAVOR/VERSION_CODE/VERSION_NAME）
func SourceGenTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("generateBuildConfig")
	t.AddDirOutputs(filepath.Join(ctx.BuildDir, "gen"))
	t.AddFileOutputs(filepath.Join(ctx.BuildDir, buildConfigMarker))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		var ns string
		var applicationID string
		var versionCode, versionName string
		flavor := ""
		buildType := "debug"
		if cfg, ok := ctx.Config.(*AppConfig); ok {
			ns = cfg.Namespace
			applicationID = cfg.ApplicationID
			versionCode = cfg.VersionCode
			versionName = cfg.VersionName + cfg.VersionNameSuffix
			if cfg.Release {
				buildType = "release"
			}
		}
		if applicationID == "" {
			applicationID = ns
		}
		if ns == "" {
			fmt.Println("警告: 未解析到 namespace，跳过 BuildConfig 生成")
			return false
		}

		vc := 1
		fmt.Sscanf(versionCode, "%d", &vc)
		pkgDir := filepath.Join(ctx.BuildDir, "gen", strings.ReplaceAll(ns, ".", "/"))
		os.MkdirAll(pkgDir, 0755)
		buildConfigPath := filepath.Join(pkgDir, "BuildConfig.java")

		var sb strings.Builder
		sb.WriteString("/** Automatically generated file. DO NOT MODIFY */\n")
		sb.WriteString("package " + ns + ";\n\n")
		sb.WriteString("public final class BuildConfig {\n")
		if applicationID != ns {
			sb.WriteString(fmt.Sprintf("  public static final String APPLICATION_ID = \"%s\";\n", applicationID))
		} else {
			sb.WriteString("  public static final String APPLICATION_ID = \"" + ns + "\";\n")
		}
		debug := "false"
		if buildType == "debug" {
			debug = "true"
		}
		sb.WriteString(fmt.Sprintf("  public static final boolean DEBUG = %s;\n", debug))
		sb.WriteString(fmt.Sprintf("  public static final String BUILD_TYPE = \"%s\";\n", buildType))
		if flavor != "" {
			sb.WriteString(fmt.Sprintf("  public static final String FLAVOR = \"%s\";\n", flavor))
		}
		sb.WriteString(fmt.Sprintf("  public static final String VERSION_CODE_STR = \"%s\";\n", versionCode))
		sb.WriteString(fmt.Sprintf("  public static final String VERSION_NAME = \"%s\";\n", versionName))
		// flavor buildConfigField（如 HAS_IJK）
		if cfg, ok := ctx.Config.(*AppConfig); ok {
			for _, f := range cfg.BuildConfigs {
				parts := strings.SplitN(f, ":", 3)
				if len(parts) != 3 {
					continue
				}
				ft, fn, fv := parts[0], parts[1], parts[2]
				// 字符串型值需带引号；布尔/数字原样
				if ft == "String" {
					fv = `"` + fv + `"`
				}
				sb.WriteString(fmt.Sprintf("  public static final %s %s = %s;\n", ft, fn, fv))
			}
		}
		sb.WriteString("}\n")

		if err := os.WriteFile(buildConfigPath, []byte(sb.String()), 0644); err != nil {
			fmt.Printf("写 BuildConfig 失败: %v\n", err)
			return false
		}
		os.WriteFile(filepath.Join(ctx.BuildDir, buildConfigMarker), []byte("ok"), 0644)
		fmt.Printf("生成 BuildConfig: %s\n", buildConfigPath)
		return true
	}
	return t
}

// RGenSourceDirs 收集 gen 目录（供编译 classpath 使用）
func RGenSourceDirs(ctx *engine.BuildContext) []string {
	return []string{filepath.Join(ctx.BuildDir, "gen")}
}