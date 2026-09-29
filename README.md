# Canter

Canter 是一个用 Go 编写的轻量级 Android 构建工具。它直接解析 Gradle 工程，自行完成依赖解析、Kotlin/Java 编译、资源处理、D8/R8 与打包签名，**不启动 Gradle daemon**，即可构建真实项目的 debug 与 release 变体并产出可安装 APK。

Canter 由早期 Python 原型 MiniBuild 重写而来，目标是对标 Gradle/AGP 的构建行为，产物与之保持高度一致。

## 特性

**配置解析**（静态解析，无需执行 Gradle）
- `settings.gradle.kts`、`build.gradle.kts`、`libs.versions.toml`、`gradle.properties`、多模块、`includeBuild`（composite build）
- `productFlavors`（buildConfigField、versionNameSuffix、proguardFiles）、`buildTypes`、`splits.abi`、`signingConfigs`
- `androidComponents.onVariants` 的 `localeFilters`、`useLegacyPackaging`、`packaging.resources.excludes`

**依赖解析**
- 全图 BFS + 版本冲突消解；Maven 版本范围归一化
- BOM 约束（显式 `platform` + POM `dependencyManagement` 中被 import 的 BOM）
- Gradle Module Metadata（`.module`）：按 consumer 属性选择变体、跟随 `available-at` 重定向（KMP `-android`）、`strictly`/`requires`/`prefer` 版本语义
- KMP 平台变体折叠；stub/空壳 AAR 处理；AAR 内嵌 `libs/*.jar` 纳入
- 解析结果缓存；优先复用 Gradle 缓存，可用 `CANTER_NO_GRADLE_CACHE=1` 完全禁用

**编译**
- `javac` + `kotlin-compiler-embeddable`，支持 Compose / Parcelize / Serialization 编译器插件
- 工具链严格使用项目声明的 Kotlin 版本；可从镜像自下载到 `~/.canter/toolchain/`；编译器运行时 classpath 按其 POM 递归解析
- 源码集感知（`main` + 选中 flavor）；多模块与 composite build 模块编译

**资源 / Manifest**
- ManifestMerger2 等价实现（节点归并、tools 指令、placeholder、类名展开）
- AAPT2 link 参数化；R.java / R.txt / BuildConfig 生成
- 数据资源（`META-INF/*.version`、`services`、`kotlin_builtins` 等）按 AGP 默认排除集与项目 excludes 合并入包

**打包 / Release**
- D8（multidex）；R8 混淆 + keep 规则（AGP 默认规则 + AAR consumer 规则 + manifest 组件 keep + 项目规则）
- 资源收缩（`isShrinkResources`）：proto link → R8 `--android-resources` → `aapt2 convert`
- `splits.abi` per-ABI APK + universal；`useLegacyPackaging` 控制 `.so` 压缩
- 签名前 `zipalign`；debug 证书与 release `signingConfigs`（支持字面量与 `System.getenv` 引用）

**增量**
- 任务输入内容哈希 + HashCache 记忆化；任务缓存按变体（buildType + flavor）隔离
- 依赖解析缓存、依赖免重复解压、库模块编译缓存

## 构建与使用

构建 Canter 自身：

```bash
# 编译并运行测试
cd /path/to/canter
go build ./...
go test ./...

# 生成可执行文件
go build -o canter ./cmd/canter
```

构建需要设置的环境变量：

```bash
export ANDROID_SDK_ROOT=/opt/android-sdk
export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
export PATH="$JAVA_HOME/bin:$ANDROID_SDK_ROOT/platform-tools:$PATH"
```

命令：

```bash
# 构建 debug
./canter assemble <project>

# 构建 release（minifyEnabled=true 时启用 R8）
./canter assemble <project> --release

# 指定 product flavor
./canter assemble <project> --flavor full

# 其它子命令
./canter parse  <project>              # 显示解析出的 Gradle 配置
./canter check  <project>              # 检查工具链与依赖
./canter deps   <project>              # 预下载依赖
./canter clean  <project>              # 清理构建产物
./canter mirror list|select|combo|speedtest|auto   # 镜像源管理
```

环境变量：

| 变量 | 作用 |
|---|---|
| `ANDROID_SDK_ROOT` | Android SDK 路径（或 `local.properties` 的 `sdk.dir`） |
| `JAVA_HOME` | JDK 路径 |
| `CANTER_NO_GRADLE_CACHE` | 非空时禁用 `~/.gradle/caches` 复用，完全走镜像 + 自有缓存 |
| `ANDROID_KEYSTORE_FILE` / `_PASSWORD` / `ANDROID_KEY_ALIAS` / `ANDROID_KEY_PASSWORD` | release 签名（项目未声明 signingConfigs 时的约定） |

自有目录：

- `~/.canter/`：镜像配置（`mirrors.json`）、依赖缓存（`cache/`）、工具链（`toolchain/`）
- `<project>/build/canter/`：构建产物（`app-<flavor>-<buildType>[-abi]-signed.apk` 等）

## 目录结构

```
canter/
├── cmd/canter/          # CLI 入口（main.go, mirror_cmd.go）
└── internal/
    ├── parser/          # Gradle 配置静态解析（settings/build.gradle.kts/catalog）
    ├── engine/          # 任务图引擎（Kahn 拓扑 + 并行、内容哈希签名、HashCache）
    ├── builder/         # 构建任务与依赖解析（编译/资源/manifest/dex/打包/签名/R8）
    │   └── pgconf/      # 内置 AGP 默认 proguard 规则
    ├── mirror/          # 镜像源管理（Maven/Google/插件/Gradle/SDK）
    ├── checker/         # 工具链检测
    └── xmldom/          # 轻量 XML DOM（Manifest 合并用）
```

## 验证结果

在完全无缓存（`~/.canter` 清空 + `CANTER_NO_GRADLE_CACHE=1`，纯镜像下载）条件下构建以下真实项目：

| 项目 | 变体 | 耗时 | 产物 |
|---|---|---|---|
| WearPomodoro | debug | ~230s | 10.6MB |
| WearPomodoro | release（R8 + 资源收缩）| ~280s | 1.4MB |
| Orbit | full-debug | 507s | 48.7MB |
| Orbit | full-release | 524s | 21.6MB |
| Orbit | lite-debug | 450s | 48.7MB |
| Orbit | lite-release | 552s | 21.6MB |

对比结论（WearPomodoro 与 Gradle 产物逐条目 diff）：
- 包名、versionName/versionCode、minSdk/targetSdk、ABI、`.so`、assets 一致
- Kotlin 类集合完全一致；剩余类数差异为构建工具产物（`android/*` 框架 stub、库 `R` 类、脱糖合成命名）
- release 的资源收缩与数据资源打包行为对齐 AGP

## 已知限制

- 构建产物层面已充分验证；**未在真机验证安装与运行时**（尤其 R8 全量收缩后的反射、动态资源引用行为）
- 构建速度由 D8/R8 主导，尚未做编译器 JVM 复用与 D8 分桶增量
- `.module` 的 `strictly` 已解析但消解器尚未按其降版
- 依赖图存在少量残余差异（如 `androidx.lifecycle:lifecycle-livedata-core-ktx`）
- 仅调试签名参与自动化验证；release 签名需项目或环境变量提供密钥

## 参考

- 详细开发过程、修复清单与逐项验证数据见交接文档：`当前工作区 的 /.monkeycode/docs/canter-handoff.md`
