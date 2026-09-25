package builder

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"canter/internal/engine"
)

// MergeResourcesTask 合并资源
func MergeResourcesTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("mergeResources")
	module := ctx.ModuleDirOrProject()
	t.AddDirInputs(filepath.Join(module, "src", "main", "res"))
	t.AddFileInputs(depsSignatureFile(ctx))
	t.AddDirOutputs(filepath.Join(ctx.BuildDir, "merged_res"))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		fmt.Println("合并资源...")
		merged := filepath.Join(ctx.BuildDir, "merged_res")
		os.MkdirAll(merged, 0755)
		module := ctx.ModuleDirOrProject()
		var resDirs []string
		// app 自身（main + flavor）与项目 library 模块的 res
		if ac, ok := ctx.Config.(*AppConfig); ok && len(ac.LibraryResDirs) > 0 {
			resDirs = append(resDirs, ac.LibraryResDirs...)
		} else {
			srcRes := filepath.Join(module, "src", "main", "res")
			if _, err := os.Stat(srcRes); err == nil {
				resDirs = append(resDirs, srcRes)
			}
		}
		depsDir := filepath.Join(ctx.BuildDir, "deps")
		if entries, err := os.ReadDir(depsDir); err == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() {
					names = append(names, e.Name())
				}
			}
			sort.Strings(names)
			for _, n := range names {
				res := filepath.Join(depsDir, n, "res")
				if info, err := os.Stat(res); err == nil && info.IsDir() {
					resDirs = append(resDirs, res)
				}
			}
		}
		fmt.Printf("  合并 %d 个资源目录\n", len(resDirs))
		if err := MergeResourceDirs(resDirs, merged); err != nil {
			fmt.Printf("资源合并失败: %v\n", err)
			return false
		}
		return true
	}
	return t
}

// Aapt2CompileTask AAPT2 编译资源
func Aapt2CompileTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("aapt2Compile")
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "merged_res"))
	t.AddDirOutputs(filepath.Join(ctx.BuildDir, "aapt2_compiled"))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		fmt.Println("AAPT2 编译资源...")
		aapt2 := filepath.Join(ctx.BuildTools, "aapt2")
		if _, err := os.Stat(aapt2); err != nil {
			fmt.Printf("aapt2 未找到: %s\n", aapt2)
			return false
		}
		compiled := filepath.Join(ctx.BuildDir, "aapt2_compiled")
		os.MkdirAll(compiled, 0755)
		args := []string{
			"compile",
			"--dir", filepath.Join(ctx.BuildDir, "merged_res"),
			"-o", filepath.Join(compiled, "resources.zip"),
		}
		cmd := exec.Command(aapt2, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("AAPT2 编译失败: %s\n", string(out))
			return false
		}
		fmt.Println("AAPT2 编译成功")
		return true
	}
	return t
}

var (
	activitySelfCloseRe   = regexp.MustCompile(`<activity\s[^>]*?/>`)
	activityPairedRe      = regexp.MustCompile(`(?s)<activity\s[^>]*?>.*?</activity>`)
	serviceSelfCloseRe    = regexp.MustCompile(`<service\s[^>]*?/>`)
	servicePairedRe       = regexp.MustCompile(`(?s)<service\s[^>]*?>.*?</service>`)
	receiverSelfCloseRe   = regexp.MustCompile(`<receiver\s[^>]*?/>`)
	receiverPairedRe      = regexp.MustCompile(`(?s)<receiver\s[^>]*?>.*?</receiver>`)
	providerSelfCloseRe   = regexp.MustCompile(`<provider\s[^>]*?/>`)
	providerPairedRe      = regexp.MustCompile(`(?s)<provider\s[^>]*?>.*?</provider>`)
)

// scanAARManifests 收集依赖 AAR 内的 AndroidManifest.xml（返回待合并的库清单）
func scanAARManifests(dir string) []string {
	var manifests []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".aar") {
			if zr, err := zip.OpenReader(path); err == nil {
				for _, f := range zr.File {
					if f.Name == "AndroidManifest.xml" {
						rc, err := f.Open()
						if err == nil {
							data, _ := io.ReadAll(rc)
							rc.Close()
							tmp := filepath.Join(filepath.Dir(path), filepath.Base(path)+".manifest.xml")
							if err := os.WriteFile(tmp, data, 0644); err == nil {
								manifests = append(manifests, tmp)
							}
						}
					}
				}
				zr.Close()
			}
		}
		return nil
	})
	return manifests
}

// Aapt2LinkTask AAPT2 链接资源（manifest 合并 + 参数化 SDK/版本注入）
func Aapt2LinkTask(ctx *engine.BuildContext) *engine.Task {
	t := engine.NewTask("aapt2Link")
	t.AddDirInputs(filepath.Join(ctx.BuildDir, "aapt2_compiled"))
	module := ctx.ModuleDirOrProject()
	manifest := filepath.Join(module, "src", "main", "AndroidManifest.xml")
	t.AddFileInputs(manifest)
	t.AddFileOutputs(filepath.Join(ctx.BuildDir, "resources.ap_"))
	t.ExecuteFunc = func(ctx *engine.BuildContext) bool {
		fmt.Println("AAPT2 链接资源...")
		aapt2 := filepath.Join(ctx.BuildTools, "aapt2")
		compiled := filepath.Join(ctx.BuildDir, "aapt2_compiled")
		output := filepath.Join(ctx.BuildDir, "resources.ap_")

		var ns, appID string
		var versionCode, versionName, minSDKStr, targetSDKStr string
		var minSDK int
		var targetSDK int
		if cfg, ok := ctx.Config.(*AppConfig); ok {
			ns = cfg.Namespace
			appID = cfg.ApplicationID
			versionCode = cfg.VersionCode
			versionName = cfg.VersionName + cfg.VersionNameSuffix
			minSDKStr = cfg.MinSDK
			targetSDKStr = cfg.TargetSDK
		}
		if appID == "" {
			appID = ns
		}
		fmt.Sscanf(minSDKStr, "%d", &minSDK)
		fmt.Sscanf(targetSDKStr, "%d", &targetSDK)

		// 步骤 A：收集依赖 manifest（AAR 解出的 + 已解压到 deps/ 的）
		var libManifests []string
		depsDir := filepath.Join(ctx.BuildDir, "deps")
		if _, err := os.Stat(depsDir); err == nil {
			libManifests = append(libManifests, scanAARManifests(depsDir)...)
		}
		home, _ := os.UserHomeDir()
		globalDeps := filepath.Join(home, ".canter", "cache", "deps")
		if _, err := os.Stat(globalDeps); err == nil {
			libManifests = append(libManifests, scanAARManifests(globalDeps)...)
		}
		// 已解压依赖目录中的 AndroidManifest.xml
		if entries, err := os.ReadDir(depsDir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					m := filepath.Join(depsDir, e.Name(), "AndroidManifest.xml")
					if _, err := os.Stat(m); err == nil {
						libManifests = append(libManifests, m)
					}
				}
			}
		}

		// 步骤 B：完整 manifest 合并（仿 ManifestMerger2）
		placeholders := map[string]string{
			"applicationId": appID,
			"versionCode":   versionCode,
			"versionName":   versionName,
		}
		res := mergeManifestWithPackage(manifest, libManifests, placeholders, minSDK, true, true, ns)
		if len(res.Errors) > 0 {
			for _, e := range res.Errors {
				fmt.Printf("manifest 合并错误: %s\n", e)
			}
			return false
		}
		for _, msg := range res.Messages {
			fmt.Printf("[%s] manifest: %s\n", msg.Severity, msg.Text)
		}

		finalManifest := filepath.Join(ctx.BuildDir, "AndroidManifest_fixed.xml")
		if err := os.WriteFile(finalManifest, []byte(res.XML), 0644); err != nil {
			fmt.Printf("写 manifest 失败: %v\n", err)
			return false
		}

		// 步骤 C：aapt2 link（参数化版本/API 注入 + 生成 R.java/R.txt）
		genDir := filepath.Join(ctx.BuildDir, "gen")
		os.MkdirAll(genDir, 0755)
		args := []string{
			"link",
			"-o", output,
			"-I", ctx.AndroidJar,
			"--manifest", finalManifest,
			"-R", filepath.Join(compiled, "resources.zip"),
			"--java", genDir,
			"--output-text-symbols", filepath.Join(ctx.BuildDir, "R.txt"),
			"--auto-add-overlay",
			"--no-version-vectors",
		}
		if minSDK > 0 {
			args = append(args, "--min-sdk-version", fmt.Sprintf("%d", minSDK))
		}
		if targetSDK > 0 {
			args = append(args, "--target-sdk-version", fmt.Sprintf("%d", targetSDK))
		}
		if versionCode != "" {
			var vc int
			if _, err := fmt.Sscanf(versionCode, "%d", &vc); err == nil && vc > 0 {
				args = append(args, "--version-code", fmt.Sprintf("%d", vc))
			}
		}
		if versionName != "" {
			args = append(args, "--version-name", versionName)
		}
		cmd := exec.Command(aapt2, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			outStr := string(out)
			if len(outStr) > 2000 {
				outStr = outStr[len(outStr)-2000:]
			}
			fmt.Printf("AAPT2 链接失败: %s\n", outStr)
			return false
		}
		return true
	}
	return t
}
