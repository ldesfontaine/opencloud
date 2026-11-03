package restic

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// La restauration ne lit que : restic ne pose aucun verrou (--no-lock), donc
// n'écrit rien dans le dépôt, et la règle du dépôt refuse de toute façon un
// DELETE sur les données et les instantanés.
func TestRestoreSnapshot_NeverWritesToTheRepository(t *testing.T) {
	repository := Repository{Path: "direct", RepositoryFile: "/s/repository", CACertFile: "/s/ca.crt", PasswordFile: "/s/repo-password", CacheDir: "/s/cache"}
	id := strings.Repeat("a", 64)
	command, err := RestoreSnapshot("/usr/bin/restic", repository, id, "/srv/data/.tmp", "/srv/data/a*b")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"restore", "--no-lock", "--target", "/srv/data/.tmp", id} {
		if !slices.Contains(command.Args, want) {
			t.Errorf("args lack %q: %v", want, command.Args)
		}
	}
	if !slices.Contains(command.Args, `/srv/data/a\*b`) {
		t.Errorf("the pattern is not escaped: %v", command.Args)
	}
	for _, forbidden := range []string{"forget", "prune", "rewrite", "--delete", "backup"} {
		if slices.Contains(command.Args, forbidden) {
			t.Errorf("a restore must not run %q", forbidden)
		}
	}
	for _, path := range []string{"snapshots/" + id, "data/" + id, "index/" + id, "keys/" + id} {
		if Allow(http.MethodDelete, "/"+path, "") == nil {
			t.Errorf("the repository accepts a DELETE on %s", path)
		}
	}
}

func TestRestoreSnapshot_RefusesAFormOutsideItsBounds(t *testing.T) {
	repository := Repository{Path: "direct", RepositoryFile: "/s/repository", CACertFile: "/s/ca.crt", PasswordFile: "/s/repo-password"}
	id := strings.Repeat("a", 64)
	cases := []struct{ snapshot, target, include string }{
		{"latest", "/t", "/a"}, {id, "relatif", "/a"}, {id, "/t", "/"}, {id, "/t", "/a/../b"}, {id, "/t", "a"},
	}
	for _, c := range cases {
		if _, err := RestoreSnapshot("/usr/bin/restic", repository, c.snapshot, c.target, c.include); err == nil {
			t.Errorf("%+v was accepted", c)
		}
	}
}
