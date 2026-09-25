package builder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"canter/internal/engine"
)

// R8PassThrough 无声明的兜底 keep-all 规则（对齐 R8Subprocess）
var r8PassThrough = []string{
	"-keepattributes *Annotation*",
	"-keep class ** { *; }",
}

// R8IgnoreWarnings 忽略缺少类引用的 warning（后者影响 dex 输出）
const r8IgnoreWarnings = "-dontwarn **"

// R8MinifyTask release 构建的 R8 混淆 + dex（替代 DexBuildTask 的高层步骤）
func R8MinifyTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("r8Minify")
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "kotlin_classes"))
	mergedJar := filepath.Join(ctx.BuildDir, "merged_classes.jar")
	t.AddFileInputs(mergedJar)
	var keepFiles []string
	if cfg, ok := ctx.Config.(*AppConfig); ok {
		keepFiles = cfg.ProguardFiles
	}
	for _, f := range keepFiles {
		t.AddFileInputs(f)
	}
	t.AddDirOutputs(filepath.Join(ctx.BuildDir, "dex"))
	t.AddFileOutputs(filepath.Join(ctx.BuildDir, "mapping.txt"))
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
		inline := strings.Join(r8PassThrough, "\n") + "\n" + r8IgnoreWarnings + "\n"
		// 汇总 keep 规则文件
		var pgConfs []string
		for _, f := range keep {
			if _, err := os.Stat(f); err == nil {
				pgConfs = append(pgConfs, f)
			}
		}
		if len(pgConfs) == 0 {
			f := filepath.Join(ctx.BuildDir, "r8-inline.pro")
			if err := os.WriteFile(f, []byte(inline), 0644); err == nil {
				pgConfs = append(pgConfs, f)
			}
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
		cmdArgs = append(cmdArgs, "--output", dexDir)
		cmdArgs = append(cmdArgs, mergedJar)

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
			if filepath.IsAbs(f) {
				result = append(result, f)
			} else {
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
	}
	return result
}