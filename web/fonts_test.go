package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Les deux polices de la direction artistique sont embarquées et servies avec
// leur type MIME, sans quoi certains navigateurs refusent le fichier.
func TestFonts_AreEmbeddedAndServedAsWoff2(t *testing.T) {
	server := newTestServer(t)
	for _, name := range []string{"geist.woff2", "geist-mono.woff2"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, server.staticBase+"fonts/"+name, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s: status %d", name, recorder.Code)
			continue
		}
		if got := recorder.Header().Get("Content-Type"); got != "font/woff2" {
			t.Errorf("%s: Content-Type %q", name, got)
		}
		if !strings.HasPrefix(recorder.Body.String(), "wOF2") {
			t.Errorf("%s: not a woff2 file", name)
		}
	}
}

func TestFonts_AreDeclaredInTheStylesheet(t *testing.T) {
	css, err := staticFiles.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`font-family: "Geist";`, `font-family: "Geist Mono";`, `url("fonts/geist.woff2")`, `url("fonts/geist-mono.woff2")`} {
		if !strings.Contains(string(css), want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
	if _, err := staticFiles.ReadFile("static/fonts/OFL.txt"); err != nil {
		t.Error("the OFL licence must travel with the fonts")
	}
}
