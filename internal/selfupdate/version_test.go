package selfupdate

import (
	"errors"
	"testing"
)

func TestParseVersion_AcceptsTagsAndBareNumbers(t *testing.T) {
	for _, text := range []string{"v0.1.2", "0.1.2"} {
		version, err := ParseVersion(text)
		if err != nil {
			t.Fatalf("%q : %v", text, err)
		}
		if version != (Version{Major: 0, Minor: 1, Patch: 2}) {
			t.Fatalf("%q = %+v", text, version)
		}
	}
	if got := (Version{Major: 1, Minor: 2, Patch: 3}).Tag(); got != "v1.2.3" {
		t.Fatalf("tag = %q", got)
	}
}

func TestParseVersion_RefusesDevelopmentBuildsAndOddNumbers(t *testing.T) {
	for _, text := range []string{"dev", "", "0.0.1-3-gabc-dirty", "1.2", "1.2.3.4", "01.2.3", "1.-2.3", "v1.2.3rc1", "3602582"} {
		if _, err := ParseVersion(text); !errors.Is(err, ErrNotAReleaseVersion) {
			t.Fatalf("%q doit être refusé, reçu %v", text, err)
		}
	}
}

func TestVersion_Compare_OrdersNumerically(t *testing.T) {
	older := Version{Major: 0, Minor: 9, Patch: 12}
	newer := Version{Major: 0, Minor: 10, Patch: 0}
	if older.Compare(newer) != -1 || newer.Compare(older) != 1 || older.Compare(older) != 0 {
		t.Fatal("0.9.12 précède 0.10.0")
	}
}

func TestVersion_IsSequentialUpgradeFrom_RefusesMinorJumpsAndDowngrades(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"0.1.0", "0.1.1", true},
		{"0.1.0", "0.1.9", true},
		{"0.1.3", "0.2.0", true},
		{"0.1.3", "0.2.7", true},
		{"0.0.4", "0.1.0", true},
		{"0.9.0", "1.0.0", true},
		{"0.9.0", "1.0.3", true},
		{"0.1.0", "0.3.0", false},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.9", false},
		{"0.9.0", "1.1.0", false},
		{"0.9.0", "2.0.0", false},
	}
	for _, current := range cases {
		from, _ := ParseVersion(current.from)
		to, _ := ParseVersion(current.to)
		if got := to.IsSequentialUpgradeFrom(from); got != current.want {
			t.Fatalf("%s → %s : séquentiel = %v, attendu %v", current.from, current.to, got, current.want)
		}
	}
}
