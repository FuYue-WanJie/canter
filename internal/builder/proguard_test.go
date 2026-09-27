package builder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const componentManifest = `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
          package="cc.star0.wear.pomodoro">
    <application android:name="cc.star0.wear.pomodoro.App">
        <activity android:name="cc.star0.wear.pomodoro.MainActivity"/>
        <activity android:name=".RelativeActivity"/>
        <service android:name="cc.star0.wear.pomodoro.TimerService"/>
        <receiver android:name="androidx.profileinstaller.ProfileInstallReceiver"/>
        <provider android:name="androidx.startup.InitializationProvider"
                  android:authorities="x"/>
    </application>
</manifest>
`

func TestManifestComponentKeepRules(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "AndroidManifest_fixed.xml")
	if err := os.WriteFile(manifestPath, []byte(componentManifest), 0644); err != nil {
		t.Fatal(err)
	}

	rules, err := manifestComponentKeepRules(manifestPath)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}

	for _, cls := range []string{
		"cc.star0.wear.pomodoro.App",
		"cc.star0.wear.pomodoro.MainActivity",
		"cc.star0.wear.pomodoro.RelativeActivity",
		"cc.star0.wear.pomodoro.TimerService",
		"androidx.profileinstaller.ProfileInstallReceiver",
		"androidx.startup.InitializationProvider",
	} {
		want := "-keep class " + cls + " { *; }"
		if !strings.Contains(rules, want) {
			t.Errorf("缺少 keep 规则: %s\n实际:\n%s", want, rules)
		}
	}
}

func TestWriteDefaultProguardFile(t *testing.T) {
	dir := t.TempDir()
	p1, err := writeDefaultProguardFile(dir, "proguard-android.txt")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := writeDefaultProguardFile(dir, "proguard-android-optimize.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{p1, p2} {
		if !fileExists(p) {
			t.Fatalf("文件未生成: %s", p)
		}
	}
	// optimize 版包含 -allowaccessmodification，普通版没有
	data, _ := os.ReadFile(p2)
	if !strings.Contains(string(data), "-allowaccessmodification") {
		t.Errorf("optimize 版应包含 -allowaccessmodification")
	}
	data, _ = os.ReadFile(p1)
	if strings.Contains(string(data), "-allowaccessmodification") {
		t.Errorf("普通版不应包含 -allowaccessmodification")
	}
}

func TestIsDefaultProguardFile(t *testing.T) {
	if !isDefaultProguardFile("proguard-android-optimize.txt") {
		t.Errorf("应识别 optimize 默认文件")
	}
	if isDefaultProguardFile("proguard-rules.pro") {
		t.Errorf("项目规则文件不应被当作默认文件")
	}
}

func TestDepConsumerRules(t *testing.T) {
	depsDir := t.TempDir()
	for _, name := range []string{"b_dep", "a_dep", "c_norules"} {
		os.MkdirAll(filepath.Join(depsDir, name), 0755)
	}
	os.WriteFile(filepath.Join(depsDir, "a_dep", "proguard.txt"), []byte("-keep class a.A"), 0644)
	os.WriteFile(filepath.Join(depsDir, "b_dep", "proguard.txt"), []byte("-keep class b.B"), 0644)
	os.WriteFile(filepath.Join(depsDir, "c_norules", "classes.jar"), []byte{0x50, 0x4b}, 0644)

	got := depConsumerRules(depsDir)
	if len(got) != 2 {
		t.Fatalf("应找到 2 份 consumer 规则，实际 %d: %v", len(got), got)
	}
	// 按路径有序
	if !strings.HasSuffix(got[0], "a_dep"+string(filepath.Separator)+"proguard.txt") {
		t.Errorf("排序错误: %v", got)
	}
}
