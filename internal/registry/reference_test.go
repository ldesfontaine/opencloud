package registry

import (
	"errors"
	"strings"
	"testing"
)

// L'hôte d'une image finit dans un compose.yml : tout caractère que Docker ne
// met pas dans un nom d'hôte est refusé, un par un.
func TestParse_RefusesAHostDockerWouldNotName(t *testing.T) {
	forbidden := []string{
		"$", "#", "{", "}", `"`, "'", "\n", "\r", "\t", ";", "&", "|", "<", ">", `\`, "`", "%", "!", "(", ")", "*", ",", "=", "?", "_", " ",
		"\x00", "é", "[", "]", "+", "~", "^",
	}
	for _, char := range forbidden {
		for _, image := range []string{
			"evil" + char + "host.example/app:1",
			"evil.example" + char + "/app:1",
			"evil.example:5000" + char + "/app:1",
		} {
			if _, err := Parse(image); !errors.Is(err, ErrBadReference) {
				t.Errorf("Parse(%q) accepted a host with %q", image, char)
			}
		}
	}

	cases := []string{
		"${DB_PASSWORD}.evil.example/app",
		"${DB_PASSWORD}.evil.example/app:1",
		"$(id).evil.example/app:1",
		"evil.example:0/app",
		"evil.example:65536/app",
		"evil.example:+80/app",
		"evil.example:-80/app",
		"evil.example:080000/app",
		"evil.example:8o/app",
		"evil.example:/app",
		"evil.example::5000/app",
		"-evil.example/app",
		"evil-.example/app",
		"evil.-example/app",
		"a..b/app",
		".evil.example/app",
		"evil.example./app",
		"[::1/app",
		"[::1]x/app",
		"[::1]:/app",
		"[::1]:0/app",
		"[::1]:65536/app",
		"[zz::1]:5000/app",
		"[fe80::1%25eth0]:5000/app",
		"[10.0.0.1]:5000/app",
		"[]:5000/app",
		"::1/app",
		"[a.b]/app",
		strings.Repeat("a", 64) + ".example/app",
		strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62) + "/app",
	}
	for _, image := range cases {
		if _, err := Parse(image); !errors.Is(err, ErrBadReference) {
			t.Errorf("Parse(%q) accepted", image)
		}
	}
}

func TestParse_AcceptsTheHostsDockerAccepts(t *testing.T) {
	cases := []struct{ image, registry string }{
		{"ghcr.io/org/app:v1", "ghcr.io"},
		{"localhost:5000/app", "localhost:5000"},
		{"localhost/app:1", "localhost"},
		{"registry.example.org:443/a/b:1", "registry.example.org:443"},
		{"10.0.0.5:5000/app", "10.0.0.5:5000"},
		{"[::1]:5000/app:1", "[::1]:5000"},
		{"[::1]/app:1", "[::1]"},
		{"[2001:db8::1]:5000/app", "[2001:db8::1]:5000"},
		{"Registry.Example.ORG/app:1", "Registry.Example.ORG"},
		{"xn--bcher-kva.example/app:1", "xn--bcher-kva.example"},
		{"my-registry.example:65535/app", "my-registry.example:65535"},
		{strings.Repeat("a", 63) + ".example/app", strings.Repeat("a", 63) + ".example"},
		{strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + "/app",
			strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)},
	}
	for _, tc := range cases {
		got, err := Parse(tc.image)
		if err != nil || got.Registry != tc.registry {
			t.Errorf("Parse(%q) = %+v, %v ; want registry %q", tc.image, got, err, tc.registry)
		}
	}
}
