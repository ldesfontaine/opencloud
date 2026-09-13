package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

var frontKey = regexp.MustCompile(`\bt\("([a-z0-9_.]+)"`)

// Chaque clé que le front demande en dur existe dans les deux catalogues :
// une clé absente s'afficherait entre crochets. Les clés composées à
// l'exécution (job.status_ + état) restent couvertes par le test de lang.
func TestFrontend_UsesOnlyKnownKeys(t *testing.T) {
	server := newTestServer(t)
	found := 0
	err := filepath.WalkDir("../../web/src", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (!strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx")) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range frontKey.FindAllStringSubmatch(string(content), -1) {
			found++
			for _, code := range lang.Codes() {
				if strings.HasPrefix(server.catalogs.For(code).Get(match[1]), "[") {
					t.Errorf("%s: la clé %q manque dans %s", path, match[1], code)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no key found: the guard no longer sees the front")
	}
}
