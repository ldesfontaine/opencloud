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
