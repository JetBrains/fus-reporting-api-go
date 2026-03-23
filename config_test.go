package fus

import "testing"

func TestParseMajorVersion(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"2024.1", []int{2024, 1}},
		{"2024.1.3", []int{2024, 1, 3}},
		{"2024", []int{2024, 0}},
		{"", nil},
		{"abc", []int{0, 0}},
	}
	for _, tt := range tests {
		got := parseMajorVersion(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseMajorVersion(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseMajorVersion(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestCompareMajorVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int // negative, zero, or positive
	}{
		{"2024.1", "2024.1", 0},
		{"2024.1", "2024.2", -1},
		{"2024.2", "2024.1", 1},
		{"2025.1", "2024.1", 1},
		{"2024.1.3", "2024.1", 1}, // longer is greater when prefix matches
		{"2024.1", "2024.1.3", -1},
	}
	for _, tt := range tests {
		a := parseMajorVersion(tt.a)
		b := parseMajorVersion(tt.b)
		got := compareMajorVersions(a, b)
		if (tt.want < 0 && got >= 0) || (tt.want > 0 && got <= 0) || (tt.want == 0 && got != 0) {
			t.Errorf("compareMajorVersions(%q, %q) = %d, want sign=%d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestAcceptVersion(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		current string
		want    bool
	}{
		{"in range", "2020.1", "2025.1", "2024.1", true},
		{"at from boundary", "2024.1", "2025.1", "2024.1", true},
		{"at to boundary (exclusive)", "2024.1", "2025.1", "2025.1", false},
		{"before from", "2024.1", "2025.1", "2023.1", false},
		{"after to", "2024.1", "2025.1", "2026.1", false},
		{"no to (open-ended)", "2020.1", "", "2024.1", true},
		{"no from (open-ended)", "", "2025.1", "2024.1", true},
		{"no from, at to", "", "2025.1", "2025.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			borders := &versionBorders{From: tt.from, To: tt.to}
			got := acceptVersion(borders, tt.current)
			if got != tt.want {
				t.Errorf("acceptVersion({%q,%q}, %q) = %v, want %v", tt.from, tt.to, tt.current, got, tt.want)
			}
		})
	}
}

func TestFindMatchingVersion(t *testing.T) {
	versions := []remoteConfigVersion{
		{
			MajorBuildVersionBorders: &versionBorders{From: "2024.1", To: "2025.1"},
			Endpoints:                remoteConfigEndpoints{Send: "https://endpoint-2024/"},
			Options:                  remoteConfigOptions{IDSalt: "salt-2024"},
		},
		{
			MajorBuildVersionBorders: &versionBorders{From: "2025.1"},
			Endpoints:                remoteConfigEndpoints{Send: "https://endpoint-2025/"},
			Options:                  remoteConfigOptions{IDSalt: "salt-2025"},
		},
	}

	v := findMatchingVersion(versions, "2024.3")
	if v == nil {
		t.Fatal("expected matching version for 2024.3")
	}
	if v.Endpoints.Send != "https://endpoint-2024/" {
		t.Errorf("matched wrong version: send=%q", v.Endpoints.Send)
	}

	v = findMatchingVersion(versions, "2025.2")
	if v == nil {
		t.Fatal("expected matching version for 2025.2")
	}
	if v.Endpoints.Send != "https://endpoint-2025/" {
		t.Errorf("matched wrong version: send=%q", v.Endpoints.Send)
	}
}

func TestConfigURLTemplate(t *testing.T) {
	all := configURLTemplate(RegionAll)
	if all != configURLTemplateAll {
		t.Errorf("RegionAll template = %q, want %q", all, configURLTemplateAll)
	}
	cn := configURLTemplate(RegionCN)
	if cn != configURLTemplateCN {
		t.Errorf("RegionCN template = %q, want %q", cn, configURLTemplateCN)
	}
}
