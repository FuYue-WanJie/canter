package builder

import (
	"os"
	"path/filepath"
	"testing"
)

const rootKMPModuleJSON = `{
  "formatVersion": "1.1",
  "component": { "group": "androidx.lifecycle", "module": "lifecycle-runtime-ktx", "version": "2.11.0" },
  "variants": [
    {
      "name": "metadataApiElements",
      "attributes": { "org.gradle.usage": "kotlin-metadata", "org.jetbrains.kotlin.platform.type": "common" },
      "dependencies": [
        { "group": "org.jetbrains.kotlin", "module": "kotlin-stdlib", "version": { "requires": "2.1.20" } }
      ]
    },
    {
      "name": "androidApiElements-published",
      "attributes": { "org.gradle.usage": "java-api", "org.jetbrains.kotlin.platform.type": "androidJvm" },
      "available-at": { "url": "../../x-android/2.11.0/x-android-2.11.0.module", "group": "androidx.lifecycle", "module": "lifecycle-runtime-ktx-android", "version": { "requires": "2.11.0" } }
    },
    {
      "name": "androidRuntimeElements-published",
      "attributes": { "org.gradle.usage": "java-runtime", "org.jetbrains.kotlin.platform.type": "androidJvm" },
      "available-at": { "url": "../../x-android/2.11.0/x-android-2.11.0.module", "group": "androidx.lifecycle", "module": "lifecycle-runtime-ktx-android", "version": { "requires": "2.11.0" } }
    },
    {
      "name": "androidSourcesElements-published",
      "attributes": { "org.gradle.usage": "java-runtime", "org.jetbrains.kotlin.platform.type": "androidJvm" },
      "available-at": { "url": "../../x-android/2.11.0/x-android-2.11.0.module", "group": "androidx.lifecycle", "module": "lifecycle-runtime-ktx-android", "version": { "requires": "2.11.0" } }
    }
  ]
}`

const androidModuleJSON = `{
  "formatVersion": "1.1",
  "component": { "group": "androidx.lifecycle", "module": "lifecycle-runtime-ktx-android", "version": "2.11.0" },
  "variants": [
    {
      "name": "androidRuntimeElements",
      "attributes": { "org.gradle.usage": "java-runtime", "org.jetbrains.kotlin.platform.type": "androidJvm" },
      "dependencies": [
        { "group": "androidx.lifecycle", "module": "lifecycle-runtime", "version": { "strictly": "2.11.0" } },
        { "group": "org.jetbrains.kotlin", "module": "kotlin-stdlib", "version": { "prefer": "2.1.20" } }
      ],
      "dependencyConstraints": [
        { "group": "androidx.annotation", "module": "annotation", "version": { "requires": "1.8.1" } }
      ]
    },
    {
      "name": "androidApiElements",
      "attributes": { "org.gradle.usage": "java-api", "org.jetbrains.kotlin.platform.type": "androidJvm" },
      "dependencies": []
    }
  ]
}`

func writeJSON(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestParseModuleDepsFollowsAvailableAt KMP 根模块的依赖经 available-at 从 -android 模块读取
func TestParseModuleDepsFollowsAvailableAt(t *testing.T) {
	fake := func(group, artifact, version string) (string, error) {
		if artifact == "lifecycle-runtime-ktx-android" {
			return writeJSON(t, t.TempDir(), "x-android.module", androidModuleJSON), nil
		}
		return writeJSON(t, t.TempDir(), "x.module", rootKMPModuleJSON), nil
	}

	deps, constraints := parseModuleDepsWithFetcher(fake, "androidx.lifecycle", "lifecycle-runtime-ktx", "2.11.0")
	if len(deps) != 2 {
		t.Fatalf("应解析出 2 个依赖（来自 -android 模块），实际 %d: %v", len(deps), deps)
	}
	if deps[0].artifact != "lifecycle-runtime" || deps[0].version != "2.11.0" {
		t.Errorf("strictly 版本应被采用: %+v", deps[0])
	}
	if deps[1].artifact != "kotlin-stdlib" || deps[1].version != "2.1.20" {
		t.Errorf("prefer 版本应被采用: %+v", deps[1])
	}
	if constraints == nil || constraints["androidx.annotation:annotation"] != "1.8.1" {
		t.Errorf("约束应来自 -android 模块: %v", constraints)
	}
}

// TestSelectModuleVariantPrefersRuntime 无 available-at 时优先 android 运行时变体并排除 sources
func TestSelectModuleVariantPrefersRuntime(t *testing.T) {
	m, err := readModuleJSON(writeJSON(t, t.TempDir(), "x.module", rootKMPModuleJSON))
	if err != nil {
		t.Fatal(err)
	}
	v := selectModuleVariant(m)
	if v == nil {
		t.Fatal("应选中变体")
	}
	if v.Name != "androidRuntimeElements-published" {
		t.Errorf("应选 androidRuntimeElements-published（运行时），实际 %s", v.Name)
	}
}
