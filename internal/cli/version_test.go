package cli

import "testing"

func TestVersionNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		// same version
		{"v1.2.7", "v1.2.7", false},
		// newer available
		{"v1.2.7", "v1.3.0", true},
		{"v1.0.0", "v1.0.1", true},
		{"v0.9.0", "v0.24.0", true},
		// already on newer
		{"v1.3.0", "v1.2.7", false},
		{"v2.0.0", "v1.9.9", false},
		// dev builds
		{"dev", "v1.2.7", true},
		{"dev", "dev", false},
		// invalid tags
		{"v1.2.7", "", false},
		{"v1.2.7", "not-a-version", false},
	}
	for _, tt := range tests {
		got := versionNewer(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("versionNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}
