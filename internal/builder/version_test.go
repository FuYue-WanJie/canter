package builder

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.11.0", "2.9.4", 1},
		{"2.9.4", "2.11.0", -1},
		{"2.11.0", "2.11.0", 0},
		{"1.0.0", "1.0", 1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-rc1", "1.0.0-beta2", 1},
		{"1.3.0-alpha07", "1.3.0", -1},
		{"1.3.0", "1.2.0", 1},
		{"2.6.1", "2.9.4", -1},
		{"35.0.0", "36.0.0", -1},
		{"37.0.0", "36.0.0", 1},
		{"1.0.0-snapshot", "1.0.0", -1},
		{"1.11.0", "1.9.0", 1},
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSelectHighestVersion(t *testing.T) {
	got := selectHighestVersion([]string{"2.6.1", "2.9.4", "2.11.0"})
	if got != "2.11.0" {
		t.Errorf("expected 2.11.0, got %s", got)
	}
	got = selectHighestVersion([]string{"1.3.0-alpha07", "1.3.0"})
	if got != "1.3.0" {
		t.Errorf("expected 1.3.0, got %s", got)
	}
}