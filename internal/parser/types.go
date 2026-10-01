package parser

// Dependency 依赖声明
type Dependency struct {
	Group       string
	Artifact    string
	Version     string
	Scope       string // implementation, api, compileOnly, runtimeOnly, etc.
	IsPlatform  bool   // platform(BOM) 声明
	IsProject   bool   // project(":xxx") 声明
	ProjectPath string
}

// AndroidConfig android { } 块配置
type AndroidConfig struct {
	Namespace                 string
	CompileSDK                *float64 // 可能为 int(35) 或 float(37.1)
	ApplicationID             string
	MinSDK                    *int
	TargetSDK                 *int
	VersionCode               *int
	VersionName               string
	TestInstrumentationRunner string
	JvmTarget                 string
	ComposeEnabled            bool
	MinifyEnabled             bool
	ShrinkResources           bool
	ProguardFiles             []string
	ABIFilters                []string
	SourceCompatibility       string
	TargetCompatibility       string
	SigningConfig             string
	BuildConfigFields         []string // 'type:name:value' 三元组（flavor buildConfigField）
	SelectedFlavor            string
	FlavorVersionNameSuffix   string
	// splits.abi 配置：SplitABIEnable/SplitABIUniversalDecl 为显式声明才生效
	SplitABIEnable        bool
	SplitABIUniversalDecl *bool // nil=未声明（Gradle 默认 true）
	SplitABIInclude       []string
	// signingConfigs 中 create("name") 块的字段（storeFile 相对项目根解析）
	SigningConfigs map[string]SigningConfigEntry
	// productFlavors 全集（首个为默认选中）
	Flavors map[string]FlavorConfig
	// androidComponents { onVariants(...) { ... } } 的变体级定制
	VariantConfigs []VariantConfig
}

// VariantConfig androidComponents.onVariants 的定制（buildType 为空表示全部变体）
type VariantConfig struct {
	BuildType          string   // withBuildType("release") 过滤；空=全部
	LocaleFilters      []string // androidResources.localeFilters.addAll(...)
	UseLegacyPackaging *bool    // packaging.jniLibs.useLegacyPackaging.set(...)
	ResourceExcludes   []string // packaging.resources.excludes.addAll(...)
}

// SigningConfigEntry 一个签名配置（storeFile 相对项目根）
type SigningConfigEntry struct {
	StoreFile     string
	StorePassword string
	KeyAlias      string
	KeyPassword   string
}

// FlavorConfig 一个 productFlavor 的定制
type FlavorConfig struct {
	VersionNameSuffix string
	BuildConfigFields []string // 'type:name:value' 三元组
	ProguardFiles     []string
}

// ModuleConfig 模块配置
type ModuleConfig struct {
	Name              string
	Path              string
	Plugins           []string
	PluginVersions    map[string]string
	Android           *AndroidConfig
	Dependencies      []Dependency
	BuildFeatures     map[string]bool
	PackagingExcludes []string
	// IncludeBuild 标记该模块来自 composite build（settings 的 includeBuild），
	// 值为 includeBuild 的根相对路径；普通 include 模块为空。
	IncludeBuild string
}

// ProjectConfig 完整项目配置
type ProjectConfig struct {
	RootDir            string
	ProjectName        string
	Modules            []ModuleConfig
	Repositories       []string
	PluginRepositories []string
	Catalog            *VersionCatalog
	GradleProperties   map[string]string
}
