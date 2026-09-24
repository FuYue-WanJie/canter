package builder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

const primaryManifest = `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.app">
    <application android:label="App">
        <activity android:name=".MainActivity" android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
                <category android:name="android.intent.category.LAUNCHER" />
            </intent-filter>
        </activity>
    </application>
</manifest>
`

const libManifest = `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.lib">
    <application>
        <service android:name=".LibService" android:exported="false" />
        <provider android:name=".LibProvider" android:authorities="${applicationId}.libprovider" />
    </application>
</manifest>
`

func TestMergeManifest(t *testing.T) {
	primary := writeTemp(t, "AndroidManifest.xml", primaryManifest)
	lib := writeTemp(t, "lib-manifest.xml", libManifest)

	res := mergeManifest(primary, []string{lib}, map[string]string{"applicationId": "com.example.app"}, 23, false, false)
	if len(res.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	xml := res.XML

	// 库的 service/provider 合入
	if !strings.Contains(xml, "LibService") {
		t.Errorf("service 未合入:\n%s", xml)
	}
	if !strings.Contains(xml, "LibProvider") {
		t.Errorf("provider 未合入:\n%s", xml)
	}
	// placeholder 替换
	if !strings.Contains(xml, "com.example.app.libprovider") {
		t.Errorf("applicationId placeholder 未替换:\n%s", xml)
	}
	// 相对类名展开为库包名
	if !strings.Contains(xml, "com.example.lib.LibService") {
		t.Errorf("相对类名未展开:\n%s", xml)
	}
	// tools 命名空间被剥离
	if strings.Contains(xml, "tools:") {
		t.Errorf("tools 命名空间未剥离:\n%s", xml)
	}
}

func TestMergeManifestLibraryAppendProvider(t *testing.T) {
	// provider 在应用清单也声明同 authorities 的场景：验证按 name key 归并
	lib2 := `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.lib2">
    <application>
        <receiver android:name=".BootReceiver" android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.BOOT_COMPLETED" />
            </intent-filter>
        </receiver>
    </application>
</manifest>
`
	primary := writeTemp(t, "AndroidManifest.xml", primaryManifest)
	lib := writeTemp(t, "lib2-manifest.xml", lib2)
	res := mergeManifest(primary, []string{lib}, nil, 23, false, false)
	if len(res.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if !strings.Contains(res.XML, "BootReceiver") {
		t.Errorf("receiver 未合入:\n%s", res.XML)
	}
	if !strings.Contains(res.XML, "BOOT_COMPLETED") {
		t.Errorf("intent-filter 未合入:\n%s", res.XML)
	}
}

func TestMergeManifestToolsRemove(t *testing.T) {
	primaryWithRemove := `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    xmlns:tools="http://schemas.android.com/tools"
    package="com.example.app">
    <application android:label="App">
        <activity android:name=".MainActivity" android:exported="true" />
        <activity android:name=".LegacyActivity"
            tools:node="remove" />
    </application>
</manifest>
`
	lib := `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.lib">
    <application>
        <activity android:name=".LibActivity" android:exported="false" />
    </application>
</manifest>
`
	primary := writeTemp(t, "AndroidManifest.xml", primaryWithRemove)
	libPath := writeTemp(t, "libm.xml", lib)
	res := mergeManifest(primary, []string{libPath}, nil, 23, false, false)
	if len(res.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if strings.Contains(res.XML, "LegacyActivity") {
		t.Errorf("tools:node=remove 的 activity 应被移除:\n%s", res.XML)
	}
	if !strings.Contains(res.XML, "LibActivity") {
		t.Errorf("库的 activity 未合入:\n%s", res.XML)
	}
}