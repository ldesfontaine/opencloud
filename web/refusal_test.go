package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Un refus s'affiche tel quel à l'opérateur : il vient du catalogue, dans sa
// langue, jamais d'une chaîne écrite dans le handler.
func TestRefusal_IsWrittenInTheOperatorLanguage(t *testing.T) {
	for _, code := range lang.Codes() {
		server := newTestServer(t)
		if err := server.saveLanguage(code); err != nil {
			t.Fatal(err)
		}
		cookie := csrfCookieFrom(t, server.Server)
		got := postLanguage(server.Server, cookie, url.Values{"csrf": {cookie.Value}, "language": {"klingon"}})
		if got.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", code, got.Code)
		}
		want := server.catalogs.For(code).Get("error.unknown_language")
		if strings.TrimSpace(got.Body.String()) != want {
			t.Errorf("%s: body %q, want %q", code, got.Body.String(), want)
		}
	}
}

var literalRefusal = regexp.MustCompile(`s\.refuse\([^)]*"`)

// Le garde-fou : aucun appel à refuse ne porte une chaîne littérale.
func TestRefusal_NeverCarriesALiteralString(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if match := literalRefusal.FindString(string(content)); match != "" {
			t.Errorf("%s: refus en dur, passer par le catalogue : %s", source, match)
		}
	}
}
