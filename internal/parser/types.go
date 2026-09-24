package parser

// Dependency 依赖声明
type Dependency struct {
	Group      string
	Artifact   string
	Version    string
	Scope      string // implementation, api, compileOnly, runtimeOnly, etc.
	IsPlatform bool   // platform(BOM) 声明
	IsProject  bool   // project(":xxx") 声明
	ProjectPath string
}

// AndroidConfig android { } 块配置
type AndroidConfig struct {
	Namespace                string
	CompileSDK               *float64 // 可能为 int(35) 或 float(37.1)
	ApplicationID            string
	MinSDK                   *int
	TargetSDK                *int
	VersionCode              *int
	VersionName              string
	TestInstrumentationRunner string
	JvmTarget                string
	ComposeEnabled           bool
	MinifyEnabled            bool
	ShrinkResources          bool
	ProguardFiles            []string
	ABIFilters               []string
	SourceCompatibility      string
	TargetCompatibility      string
	SigningConfig            string
	BuildConfigFields        []string // 'type:name:value' 三元组（flavor buildConfigField）
	SelectedFlavor           string
}

// ModuleConfig 模块配置
type ModuleConfig struct {
	Name           string
	Path           string
	Plugins        []string
	PluginVersions map[string]string
	Android        *AndroidConfig
	Dependencies   []Dependency
	BuildFeatures  map[string]bool
	PackagingExcludes []string
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
