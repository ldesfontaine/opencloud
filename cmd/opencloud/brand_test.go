package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// Le nom du binaire n'est pas dérivé de l'identifiant : il suit ce
// répertoire. Si l'un bouge sans l'autre, les commandes que le serveur
// montre à l'opérateur ne se lancent plus.
func TestCommand_FollowsTheDirectoryName(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(dir); got != brand.Command {
		t.Errorf("cmd directory is %q, brand.Command is %q", got, brand.Command)
	}
}
