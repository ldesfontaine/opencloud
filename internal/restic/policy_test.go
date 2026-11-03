package restic

import (
	"errors"
	"strings"
	"testing"
)

var (
	sampleName = strings.Repeat("ab", 32)
	otherName  = strings.Repeat("0f", 32)
)

// Le tableau de l'essai, ligne par ligne, puis les formes hostiles.
func TestAllow_FollowsTheRouteTable(t *testing.T) {
	cases := []struct {
		method, path, query string
		want                error
	}{
		{"POST", "/", "create=true", nil},
		{"DELETE", "/", "", DeleteRepositoryRefused},
		{"DELETE", "/", "create=true", DeleteRepositoryRefused},
		{"GET", "/", "", MethodRefused},
		{"POST", "/", "", QueryRefused},
		{"POST", "/", "create=false", QueryRefused},
		{"POST", "/", "create=true&x=1", QueryRefused},

		{"HEAD", "/config", "", nil},
		{"GET", "/config", "", nil},
		{"POST", "/config", "", nil},
		{"DELETE", "/config", "", DeleteRefused},
		{"PUT", "/config", "", MethodRefused},

		{"GET", "/data/", "", nil},
		{"GET", "/keys/", "", nil},
		{"GET", "/locks/", "", nil},
		{"GET", "/snapshots/", "", nil},
		{"GET", "/index/", "", nil},
		{"POST", "/data/", "", MethodRefused},
		{"DELETE", "/data/", "", DeleteRefused},

		{"HEAD", "/data/" + sampleName, "", nil},
		{"GET", "/snapshots/" + sampleName, "", nil},
		{"POST", "/index/" + sampleName, "", nil},
		{"DELETE", "/locks/" + sampleName, "", nil},
		{"DELETE", "/data/" + sampleName, "", DeleteRefused},
		{"DELETE", "/snapshots/" + sampleName, "", DeleteRefused},
		{"DELETE", "/keys/" + sampleName, "", DeleteRefused},
		{"DELETE", "/index/" + sampleName, "", DeleteRefused},
		{"DELETE", "/locks/" + sampleName, "force=1", QueryRefused},
		{"PATCH", "/data/" + sampleName, "", MethodRefused},
		{"GET", "/data/" + sampleName, "x=1", QueryRefused},
	}
	for _, c := range cases {
		got := Allow(c.method, c.path, c.query)
		if got != c.want {
			t.Errorf("%s %s?%s: got %v, want %v", c.method, c.path, c.query, got, c.want)
		}
	}
}

func TestAllow_RefusesHostilePaths(t *testing.T) {
	hostile := []string{
		"",
		"config",
		"//config",
		"/../repo-a/config",
		"/%2e%2e/repo-a/config",
		"/data/%2e%2e",
		"/data/../config",
		"/data//" + sampleName,
		"/data/" + sampleName + "/",
		"/data/" + sampleName + "/x",
		"/data/" + strings.ToUpper(sampleName),
		"/data/" + sampleName[:63],
		"/data/" + sampleName + "0",
		"/data/" + strings.Repeat("zz", 32),
		"/data/ab/" + sampleName,
		"/repo-a/config",
		"/other/",
		"/config/",
		"/Data/",
		"/data",
		"/locks/" + otherName + "%00",
	}
	for _, path := range hostile {
		for _, method := range []string{"GET", "DELETE"} {
			if got := Allow(method, path, ""); !errors.Is(got, PathRefused) {
				t.Errorf("%s %q: %v", method, path, got)
			}
		}
	}
}

// Le code part tel quel dans la réponse : il est figé.
func TestRefusal_CodesAreFrozen(t *testing.T) {
	codes := map[Refusal]string{
		DeleteRefused:           "delete_refused",
		DeleteRepositoryRefused: "delete_repository_refused",
		PathRefused:             "path_refused",
		QueryRefused:            "query_refused",
		MethodRefused:           "method_refused",
	}
	for refusal, code := range codes {
		if refusal.Error() != code {
			t.Errorf("%s", refusal)
		}
	}
}
