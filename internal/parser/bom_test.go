package parser

import "testing"

func TestBOMResolveMaterial3(t *testing.T) {
	config := GradleConfigParser{}.Parse("/workspace/Orbit")
	for _, mod := range config.Modules {
		if mod.Name != "app" {
			continue
		}
		for _, dep := range mod.Dependencies {
			if dep.Group == "androidx.compose.material3" && dep.Artifact == "material3" {
				t.Logf("material3: %s", dep.Version)
				if dep.Version == "2026.06.01" || dep.Version == "" {
					t.Errorf("material3 应解析为 BOM 中的 1.4.0")
				}
			}
		}
	}
}
