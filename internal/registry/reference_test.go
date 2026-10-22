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

// Le premier segment n'est un hôte que s'il porte un point, un port ou vaut
// « localhost » : sinon c'est un espace de noms du Hub, et la règle de l'hôte
// ne le touche pas. Les références sont celles que le dépôt et ses
// utilisateurs écrivent.
func TestParse_TheFirstSegmentIsAHostOnlyWhenDockerSaysSo(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	cases := []struct{ image, registry, repository string }{
		{"nginx:1", DockerHub, "library/nginx"},
		{"nginx", DockerHub, "library/nginx"},
		{"library/nginx", DockerHub, "library/nginx"},
		{"docker.io/nginx", DockerHub, "library/nginx"},
		{"index.docker.io/library/nginx:1.27", DockerHub, "library/nginx"},
		{"traefik/whoami:v1.10", DockerHub, "traefik/whoami"},
		{"keinos/sqlite3:latest", DockerHub, "keinos/sqlite3"},
		{"a/b/c:1", DockerHub, "a/b/c"},
		{"nginx@" + digest, DockerHub, "library/nginx"},
		{"ghcr.io/o/r", "ghcr.io", "o/r"},
		{"ghcr.io/o/r:1@" + digest, "ghcr.io", "o/r"},
		{"registry.example.com:5000/a/b", "registry.example.com:5000", "a/b"},
		{"registre.exemple.fr:5000/equipe/app", "registre.exemple.fr:5000", "equipe/app"},
		{"localhost:5000/x", "localhost:5000", "x"},
		{"localhost/x", "localhost", "x"},
		{"10.0.0.1:5000/x", "10.0.0.1:5000", "x"},
		{"myregistry:5000/app", "myregistry:5000", "app"},
		{"quay.io/keycloak/keycloak:26.0", "quay.io", "keycloak/keycloak"},
		{"public.ecr.aws/docker/library/redis:7", "public.ecr.aws", "docker/library/redis"},
		{"registry.gitlab.com/group/sub/proj:latest", "registry.gitlab.com", "group/sub/proj"},
		{"lscr.io/linuxserver/sonarr:latest", "lscr.io", "linuxserver/sonarr"},
		{"host.docker.internal:5000/x", "host.docker.internal:5000", "x"},
	}
	for _, tc := range cases {
		got, err := Parse(tc.image)
		if err != nil || got.Registry != tc.registry || got.Repository != tc.repository {
			t.Errorf("Parse(%q) = %+v, %v ; want %s / %s", tc.image, got, err, tc.registry, tc.repository)
		}
	}
	// Un premier segment sans point ni port est un espace de noms : un
	// caractère interdit y est refusé par la règle du dépôt, pas lu comme hôte.
	for _, image := range []string{"evil$host/app", "Evil/app", "evil_/app"} {
		if _, err := Parse(image); !errors.Is(err, ErrBadReference) {
			t.Errorf("Parse(%q) accepted", image)
		}
	}
}
