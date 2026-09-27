package builder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"canter/internal/engine"
)

// R8IgnoreWarnings 忽略缺少类引用的 warning（后者影响 dex 输出）
const r8IgnoreWarnings = "-dontwarn **"

// writeInlineRuleFile 写入一段内联规则并返回路径（写失败返回空）
func writeInlineRuleFile(buildDir, name, content string) string {
	out := filepath.Join(buildDir, "pgconf", name)
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return ""
	}
	if err := os.WriteFile(out, []byte(content), 0644); err != nil {
		return ""
	}
	return out
}

// R8MinifyTask release 构建的 R8 混淆 + dex（替代 DexBuildTask 的高层步骤）
func R8MinifyTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("r8Minify")
	t.AddFileInputs(filepath.Join(ctx.BuildDir, "merged_classes.jar"))
	// manifest 与依赖集合参与签名：组件 keep 规则与 consumer 规则由二者派生
	t.AddFileInputs(filepath.Join(ctx.BuildDir, "AndroidManifest_fixed.xml"))
	t.AddFileInputs(depsSignatureFile(ctx))
	if cfg, ok := ctx.Config.(*AppConfig); ok {
		for _, f := range cfg.ProguardFiles {
			// 默认规则文件生成到构建目录，以其内容参与签名
			if isDefaultProguardFile(f) {
				if def, err := writeDefaultProguardFile(ctx.BuildDir, f); err == nil {
					t.AddFileInputs(def)
				}
				continue
			}
			t.AddFileInputs(f)
		}
	}
	t.AddDirOutputs(filepath.Join(ctx.BuildDir, "dex"))
	t.AddFileOutputs(filepath.Join(ctx.BuildDir, "mapping.txt"))
	// 资源收缩：输入 proto 资源包，输出收缩后的 proto 包
	shrinkRes := false
	if cfg, ok := ctx.Config.(*AppConfig); ok && cfg.Release && cfg.ShrinkResources {
		shrinkRes = true
		t.AddFileInputs(filepath.Join(ctx.BuildDir, "resources-proto.zip"))
		t.AddFileOutputs(filepath.Join(ctx.BuildDir, "resources-shrunk.zip"))
	}
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		fmt.Println("R8 混淆 + dex...")
		// 新版 build-tools 不再单独提供 r8.jar，R8 与 D8 同打包在 lib/d8.jar
		r8Jar := filepath.Join(ctx.BuildTools, "lib", "r8.jar")
		if _, err := os.Stat(r8Jar); err != nil {
			alt := filepath.Join(ctx.BuildTools, "lib", "d8.jar")
			if _, err := os.Stat(alt); err != nil {
				fmt.Printf("r8/d8 jar 未找到: %s 或 %s\n", r8Jar, alt)
				return false
			}
			r8Jar = alt
		}
		dexDir := filepath.Join(ctx.BuildDir, "dex")
		os.MkdirAll(dexDir, 0755)

		keep := expandKeepFiles(ctx)
		// 汇总 keep 规则文件：项目规则 + AGP 默认规则 + AAR consumer 规则 + manifest 组件 keep。
		// 默认规则文件（如 proguard-android-optimize.txt）是 vendor 进仓库的内容，
		// 写到 pgconf 后同时作为任务输入参与签名。
		var pgConfs []string
		for _, f := range keep {
			if isDefaultProguardFile(f) {
				def, err := writeDefaultProguardFile(ctx.BuildDir, f)
				if err != nil {
					fmt.Printf("警告: 默认 proguard 规则生成失败: %v\n", err)
					continue
				}
				pgConfs = append(pgConfs, def)
				continue
			}
			if _, err := os.Stat(f); err == nil {
				pgConfs = append(pgConfs, f)
			} else {
				fmt.Printf("警告: proguard 文件未找到: %s\n", f)
			}
		}
		pgConfs = append(pgConfs, depConsumerRules(filepath.Join(ctx.BuildDir, "deps"))...)
		if manifestKeep, err := writeComponentKeepRules(
			ctx.BuildDir, filepath.Join(ctx.BuildDir, "AndroidManifest_fixed.xml")); err == nil && manifestKeep != "" {
			pgConfs = append(pgConfs, manifestKeep)
		}
		// 缺失引用告警按 AGP 惯例放行（依赖存在少量无伤大雅的缺失引用）
		if f := writeInlineRuleFile(ctx.BuildDir, "r8-dontwarn.pro", r8IgnoreWarnings+"\n"); f != "" {
			pgConfs = append(pgConfs, f)
		}

		minApi := "23"
		if cfg, ok := ctx.Config.(*AppConfig); ok && cfg.MinSDK != "" {
			minApi = cfg.MinSDK
		}

		cmdArgs := []string{
			"-cp", r8Jar, "com.android.tools.r8.R8",
			"--release",
			"--min-api", minApi,
		}
		if _, err := os.Stat(ctx.AndroidJar); err == nil {
			cmdArgs = append(cmdArgs, "--lib", ctx.AndroidJar)
		}
		for _, f := range pgConfs {
			cmdArgs = append(cmdArgs, "--pg-conf", f)
		}
		mapping := filepath.Join(ctx.BuildDir, "mapping.txt")
		cmdArgs = append(cmdArgs, "--pg-map-output", mapping)
		if shrinkRes {
			protoIn := filepath.Join(ctx.BuildDir, "resources-proto.zip")
			if _, err := os.Stat(protoIn); err == nil {
				cmdArgs = append(cmdArgs,
					"--android-resources", protoIn, filepath.Join(ctx.BuildDir, "resources-shrunk.zip"))
			}
		}
		cmdArgs = append(cmdArgs, "--output", dexDir)
		cmdArgs = append(cmdArgs, filepath.Join(ctx.BuildDir, "merged_classes.jar"))

		javaBin := "java"
		if ctx.JavaHome != "" {
			javaBin = filepath.Join(ctx.JavaHome, "bin", "java")
		}
		cmd := exec.Command(javaBin, append([]string{jvmXmxFlag()}, cmdArgs...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			outStr := string(out)
			if len(outStr) > 3000 {
				outStr = outStr[len(outStr)-3000:]
			}
			fmt.Printf("R8 失败: %s\n", outStr)
			return false
		}
		return true
	}
	return t
}

// expandKeepFiles 扩展 keep 文件路径（相对于项目根/模块根解析绝对路径）
func expandKeepFiles(ctx *engine.BuildContext) []string {
	var result []string
	if cfg, ok := ctx.Config.(*AppConfig); ok {
		for _, f := range cfg.ProguardFiles {
			// getDefaultProguardFile 抓到的裸文件名原样透传，由调用方展开为内置规则
			if isDefaultProguardFile(f) {
				result = append(result, f)
				continue
			}
			if filepath.IsAbs(f) {
				result = append(result, f)
				continue
			}
			candidates := []string{
				filepath.Join(ctx.ProjectDir, f),
				filepath.Join(ctx.ModuleDirOrProject(), f),
			}
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					result = append(result, c)
					break
				}
			}
		}
	}
	return result
}
