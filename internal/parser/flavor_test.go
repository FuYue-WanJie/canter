package parser

import "testing"

func TestBuildConfigFieldParse(t *testing.T) {
	content := `android {
    flavorDimensions += "player"
    productFlavors {
        create("full") {
            dimension = "player"
            buildConfigField("boolean", "HAS_IJK", "true")
        }
        create("lite") {
            dimension = "player"
            buildConfigField("boolean", "HAS_IJK", "false")
        }
    }
}`
	p := BuildFileParser{Script: GradleScriptParser{}}
	module := ModuleConfig{Name: "app", Path: "/tmp/x"}
	data := p.Script.StripComments(content)
	p.parseAndroidBlock(data, &module)
	if module.Android == nil {
		t.Fatal("android 块未解析")
	}
	t.Logf("SelectedFlavor: %q", module.Android.SelectedFlavor)
	t.Logf("BuildConfigFields: %v", module.Android.BuildConfigFields)
	if len(module.Android.BuildConfigFields) == 0 {
		t.Error("buildConfigField 未解析")
	}
}
