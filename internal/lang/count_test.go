package lang

import "testing"

func TestFormatCount_GroupsThousandsLikeTheLanguage(t *testing.T) {
	cases := []struct {
		code  Code
		count int
		want  string
	}{
		{French, 0, "0"}, {French, 999, "999"}, {French, 1204, "1 204"}, {French, 1234567, "1 234 567"},
		{English, 1204, "1,204"}, {English, 100000, "100,000"}, {English, -1204, "-1,204"},
	}
	for _, c := range cases {
		if got := FormatCount(c.code, c.count); got != c.want {
			t.Errorf("FormatCount(%s, %d) = %q, want %q", c.code, c.count, got, c.want)
		}
	}
}

func TestCounted_PicksTheSingularForm(t *testing.T) {
	catalogs := Catalogs{
		French:  {code: French, strings: map[string]string{"x": "%s essais sur %s", "x_one": "%s essai sur %s"}},
		English: {code: English, strings: map[string]string{"x": "%s results of %s", "x_one": "%s result of %s"}},
	}
	cases := []struct {
		code  Code
		count int
		want  string
	}{
		{French, 1, "1 essai sur 3"}, {French, 0, "0 essai sur 3"}, {French, 2, "2 essais sur 3"},
		{French, 1204, "1 204 essais sur 3"}, {English, 1, "1 result of 3"}, {English, 0, "0 results of 3"},
		{English, 1204, "1,204 results of 3"},
	}
	for _, c := range cases {
		if got := catalogs[c.code].Counted("x", c.count, "3"); got != c.want {
			t.Errorf("Counted(%s, %d) = %q, want %q", c.code, c.count, got, c.want)
		}
	}
}
