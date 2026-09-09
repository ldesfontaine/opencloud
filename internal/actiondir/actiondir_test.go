package actiondir

import "testing"

func TestValidID_AcceptsTheNarrowFormOnly(t *testing.T) {
	for _, id := range []string{"a", "diagnostiquer-20260906-abc", "0123456789012345678901234567890123456789"} {
		if !ValidID(id) {
			t.Errorf("%q devrait être accepté", id)
		}
	}
	for _, id := range []string{"", "A", "a b", "../x", "a/b", "a.b", "01234567890123456789012345678901234567890", "é"} {
		if ValidID(id) {
			t.Errorf("%q devrait être refusé", id)
		}
	}
}

func TestDirAndUnitName_DeriveFromTheIDOnly(t *testing.T) {
	if got := Dir("abc-1"); got != "/var/lib/opencloud/actions/abc-1" {
		t.Fatalf("Dir = %q", got)
	}
	if got := UnitName("abc-1"); got != "oc-action-abc-1" {
		t.Fatalf("UnitName = %q", got)
	}
}

func TestParseTimeout_DigitsWithinBoundsOnly(t *testing.T) {
	for content, want := range map[string]int{"1": 1, "1800\n": 1800, "86400": 86400} {
		if got, ok := ParseTimeout(content); !ok || got != want {
			t.Errorf("ParseTimeout(%q) = %d, %v ; attendu %d", content, got, ok, want)
		}
	}
	for _, content := range []string{"", "0", "86401", "-5", "12a", " 12", "12\n\n", "999999", "1e3"} {
		if _, ok := ParseTimeout(content); ok {
			t.Errorf("ParseTimeout(%q) devrait refuser", content)
		}
	}
}

func TestParsePurged_ReadsTheLauncherLineAndIgnoresTheRest(t *testing.T) {
	if got, ok := ParsePurged(FormatPurged(7)); !ok || got != 7 {
		t.Errorf("ParsePurged(FormatPurged(7)) = %d, %v", got, ok)
	}
	// La ligne se lit même si le lanceur ou sudo ont écrit autre chose avant.
	mixed := "sudo: quelque chose\n" + FormatPurged(2)
	if got, ok := ParsePurged(mixed); !ok || got != 2 {
		t.Errorf("ParsePurged(%q) = %d, %v", mixed, got, ok)
	}
	for _, output := range []string{"", "purged-directories: 0", "purged-directories: -1",
		"purged-directories: beaucoup", "purged-directories:", "rien à dire"} {
		if _, ok := ParsePurged(output); ok {
			t.Errorf("ParsePurged(%q) devrait refuser", output)
		}
	}
}
