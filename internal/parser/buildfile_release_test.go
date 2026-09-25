package parser

import (
	"os"
	"path/filepath"
	"testing"
)

const releaseBuildFile = `
import com.android.build.api.variant.FilterConfiguration

val hasReleaseSigning = false

android {
    namespace = "cc.star0.wear.pomodoro"
    compileSdk {
        version = release(37) {
            minorApiLevel = 0
        }
    }

    defaultConfig {
        applicationId = "cc.star0.wear.pomodoro"
        minSdk = 25
        targetSdk = 37
        versionCode = 5
        versionName = "0.3.2"
    }

    splits {
        abi {
            isEnable = true
            reset()
            include("armeabi-v7a", "arm64-v8a")
            isUniversalApk = true
        }
    }

    signingConfigs {
        if (hasReleaseSigning) {
            create("release") {
                storeFile = rootProject.file("x")
            }
        }
    }

    buildTypes {
        release {
            signingConfig = signingConfigs.findByName("release")
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
            )
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

androidComponents {
    onVariants(selector().withBuildType("release")) { variant ->
        variant.packaging.jniLibs.useLegacyPackaging.set(true)
    }
}
`

func TestParseReleaseMinifyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "build.gradle.kts")
	if err := os.WriteFile(path, []byte(releaseBuildFile), 0644); err != nil {
		t.Fatal(err)
	}
	mod := BuildFileParser{}.Parse(path, NewVersionCatalog(), map[string]string{})
	if mod.Android == nil {
		t.Fatal("Android 配置未解析")
	}
	if !mod.Android.MinifyEnabled {
		t.Errorf("isMinifyEnabled 应为 true")
	}
	if !mod.Android.ShrinkResources {
		t.Errorf("isShrinkResources 应为 true")
	}
	if mod.Android.CompileSDK == nil || *mod.Android.CompileSDK != 37 {
		t.Errorf("compileSdk 应为 37，实际 %v", mod.Android.CompileSDK)
	}
	if len(mod.Android.ProguardFiles) != 1 || mod.Android.ProguardFiles[0] != "proguard-android-optimize.txt" {
		t.Errorf("proguardFiles 解析异常: %v", mod.Android.ProguardFiles)
	}
}
