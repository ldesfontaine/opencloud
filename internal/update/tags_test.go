package update

import "testing"

// Un dépôt à la fois : la liste des tags d'alpine, celle de traefik, celle
// de postgres, celle de nginx.
func TestNewer_KeepsTheShapeOfTheCurrentTag(t *testing.T) {
	alpine := []string{"2.6", "3.19", "3.20", "3.21", "3.22", "3.22.1", "4.0.0-rc1", "latest", "edge", "20240101"}
	traefik := []string{"v3", "v4", "3", "latest", "v3.5.0", "v4.0.1"}
	postgres := []string{"16", "16-bookworm", "17-bookworm", "16.4", "17.0", "16-alpine"}
	nginx := []string{"1.25.3-alpine", "1.27.0-alpine", "1.27.0", "1.27.0-slim", "1.26.0", "1.26.0-slim", "608111629", "mainline"}
	cases := []struct {
		current string
		tags    []string
		want    string
	}{
		{"3.20", alpine, "3.22"},
		{"3.22", alpine, ""},
		{"3.22.1", alpine, ""},
		{"2.6", alpine, "3.22"},
		{"latest", alpine, ""},
		{"edge", alpine, ""},
		{"v3", traefik, "v4"},
		{"3", traefik, ""},
		{"v3.5.0", traefik, "v4.0.1"},
		{"16-bookworm", postgres, "17-bookworm"},
		{"16", postgres, ""},
		{"16.4", postgres, "17.0"},
		{"1.25.3-alpine", nginx, "1.27.0-alpine"},
		{"1.26.0", nginx, "1.27.0"},
		{"1.26.0-slim", nginx, "1.27.0-slim"},
		{"1.2-rc1", nginx, ""},
	}
	for _, tc := range cases {
		if got := Newer(tc.current, tc.tags); got != tc.want {
			t.Errorf("Newer(%q) = %q, want %q", tc.current, got, tc.want)
		}
	}
}

func TestNewer_IgnoresBuildIdentifiers(t *testing.T) {
	if got := Newer("v3", []string{"v3", "v4", "v608111629"}); got != "v4" {
		t.Fatalf("got %q", got)
	}
	if got := Newer("1.27.0", []string{"1.27.0", "1234567890.0.0"}); got != "" {
		t.Fatalf("ten digits accepted: %q", got)
	}
}

func TestClassify_NamesTheFirstComponentThatMoves(t *testing.T) {
	cases := []struct {
		from, to string
		want     Kind
	}{
		{"3.20", "3.22", KindMinor},
		{"3.20", "4.0", KindMajor},
		{"1.25.3", "1.25.4", KindPatch},
		{"1.25.3-alpine", "1.26.0-alpine", KindMinor},
		{"v3", "v4", KindMajor},
		{"16-bookworm", "17-bookworm", KindMajor},
		{"3.22", "3.20", ""},
		{"3.22", "3.22", ""},
		{"latest", "3.22", ""},
		{"3.22", "3.22.1", ""},
	}
	for _, tc := range cases {
		if got := Classify(tc.from, tc.to); got != tc.want {
			t.Errorf("Classify(%q, %q) = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
}
