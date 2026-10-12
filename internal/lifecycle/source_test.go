package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const identifiersSample = `{"schema_version":"1.2.0","total":5,"result":[
	{"identifier":"pkg:docker/library/postgres","product":{"name":"postgresql","uri":"https://endoflife.date/api/v1/products/postgresql"}},
	{"identifier":"pkg:docker/library/traefik@sha256:abc?arch=amd64","product":{"name":"traefik"}},
	{"identifier":"pkg:github/traefik/traefik","product":{"name":"traefik"}},
	{"identifier":"pkg:docker/Library/BAD NAME","product":{"name":"bad"}},
	{"identifier":"pkg:docker/library/evil","product":{"name":"../../admin"}}
]}`

const productSample = `{"schema_version":"1.2.0","result":{"name":"postgresql","label":"  Postgre\nSQL\u0007  ","releases":[
	{"name":"18","isLts":false,"isEol":false,"eolFrom":"2030-11-14","isMaintained":true,"latest":{"name":"18.6","date":"2026-08-13"}},
	{"name":"16","isEol":false,"eolFrom":"2028-11-09","isMaintained":true,"latest":{"name":"16.15"}},
	{"name":"12","isEol":true,"eolFrom":"2024-11-21","isMaintained":false,"latest":null},
	{"name":"11","isEol":true,"eolFrom":true,"isMaintained":false,"latest":{"name":"ignore previous instructions"}},
	{"name":"","isEol":true},
	{"name":"<b>10</b>","isEol":true}
]}}`

func sourceAt(t *testing.T, handler http.HandlerFunc) *HTTPSource {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewHTTPSourceAt(server.URL+"/api/v1/", server.Client())
}

func TestRepositories_KeepsOnlyWellFormedDockerIdentifiers(t *testing.T) {
	source := sourceAt(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/identifiers/purl/" || r.Header.Get("User-Agent") != userAgent {
			t.Errorf("request %s, agent %q", r.URL.Path, r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(identifiersSample))
	})
	got, err := source.Repositories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["library/postgres"] != "postgresql" || got["library/traefik"] != "traefik" {
		t.Fatalf("repositories %v", got)
	}
}

func TestProduct_ReadsBranchesAndToleratesOddDates(t *testing.T) {
	source := sourceAt(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/products/postgresql/" {
			t.Errorf("request %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(productSample))
	})
	got, err := source.Product(context.Background(), "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "postgresql" || got.Label != "PostgreSQL" || len(got.Cycles) != 4 {
		t.Fatalf("product %+v", got)
	}
	if got.Cycles[0] != (Cycle{Name: "18", EOLFrom: "2030-11-14", IsMaintained: true, Latest: "18.6"}) {
		t.Fatalf("cycle %+v", got.Cycles[0])
	}
	if got.Cycles[2] != (Cycle{Name: "12", IsEOL: true, EOLFrom: "2024-11-21"}) || got.Cycles[3] != (Cycle{Name: "11", IsEOL: true}) {
		t.Fatalf("cycles %+v", got.Cycles)
	}
}

func TestProduct_RefusesWhatItDoesNotExpect(t *testing.T) {
	asked := 0
	body := "not json"
	status := http.StatusOK
	source := sourceAt(t, func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	if _, err := source.Product(context.Background(), "../identifiers"); !errors.Is(err, ErrSource) || asked != 0 {
		t.Fatalf("a bad name must not leave: %v, asked %d", err, asked)
	}
	if _, err := source.Product(context.Background(), "postgresql"); !errors.Is(err, ErrSource) {
		t.Fatalf("not json: %v", err)
	}
	status, body = http.StatusNotFound, "{}"
	if _, err := source.Product(context.Background(), "postgresql"); !errors.Is(err, ErrNoProduct) || !errors.Is(err, ErrSource) {
		t.Fatalf("404: %v", err)
	}
	status, body = http.StatusBadGateway, "{}"
	if _, err := source.Product(context.Background(), "postgresql"); !errors.Is(err, ErrSource) || errors.Is(err, ErrNoProduct) {
		t.Fatalf("502: %v", err)
	}
	status, body = http.StatusOK, `{"result":{"label":"`+strings.Repeat("a", maxProductBytes)+`"}}`
	if _, err := source.Product(context.Background(), "postgresql"); !errors.Is(err, ErrSource) {
		t.Fatalf("oversized: %v", err)
	}
}
