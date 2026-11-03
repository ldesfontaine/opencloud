package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Une adresse locale n'est une erreur que si la page, elle, est publique :
// seul ce cas porte une suggestion, l'essai local reste muet.
func TestResolvePublicURL_SuggestsOnlyWhenThePageIsPublic(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		host       string
		want       publicAddress
	}{
		{"essai local, rien de configuré", "", "localhost:8080", publicAddress{URL: "http://localhost:8080", Local: true}},
		{"public_url local, page locale", "http://192.168.1.10:8080", "localhost:8080", publicAddress{URL: "http://192.168.1.10:8080", Local: true}},
		{"public_url local, page publique", "http://localhost:8080/", "cloud.exemple.fr", publicAddress{URL: "http://localhost:8080", Local: true, Suggestion: "http://cloud.exemple.fr"}},
		{"public_url public", "https://cloud.exemple.fr", "localhost:8080", publicAddress{URL: "https://cloud.exemple.fr"}},
		{"rien de configuré, page publique", "", "cloud.exemple.fr", publicAddress{URL: "http://cloud.exemple.fr"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := &Server{publicURL: c.configured}
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Host = c.host
			if got := server.resolvePublicURL(request); got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}
