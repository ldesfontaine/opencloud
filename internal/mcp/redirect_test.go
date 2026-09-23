package mcp

import (
	"errors"
	"strings"
	"testing"
)

func TestRedirectAllowed_LoopbackAlwaysDeclaredExactly(t *testing.T) {
	declared := []string{"https://claude.ai/api/mcp/auth_callback"}
	allowed := []string{
		"http://localhost:33418/oauth/callback",
		"http://127.0.0.1:9999/whatever",
		"https://[::1]/cb",
		"https://claude.ai/api/mcp/auth_callback",
	}
	for _, uri := range allowed {
		if !redirectAllowed(uri, declared) {
			t.Errorf("%s refused", uri)
		}
	}
	refused := []string{
		"",
		"/relative",
		"ftp://localhost/x",
		"https://claude.ai/api/mcp/auth_callback/",
		"https://claude.ai/api/mcp/auth_callback?x=1",
		"https://evil.example/cb",
		"http://localhost.evil.example/cb",
		"https://claude.ai/api/mcp/auth_callback#frag",
	}
	for _, uri := range refused {
		if redirectAllowed(uri, declared) {
			t.Errorf("%s accepted", uri)
		}
	}
}

func TestValidateRedirectURIs_TrimsAndRefusesMalformed(t *testing.T) {
	cleaned, err := ValidateRedirectURIs([]string{" https://a.example/cb ", "", "http://b.example/cb"})
	if err != nil || len(cleaned) != 2 || cleaned[0] != "https://a.example/cb" {
		t.Fatalf("%v %v", cleaned, err)
	}
	for _, bad := range []string{"a.example/cb", "https:///cb", "https://a.example/cb#x", "https://" + strings.Repeat("a", MaxRedirectURILen) + ".example"} {
		if _, err := ValidateRedirectURIs([]string{bad}); !errors.Is(err, ErrRedirectURIInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	tooMany := make([]string, MaxRedirectURIs+1)
	for i := range tooMany {
		tooMany[i] = "https://a.example/cb"
	}
	if _, err := ValidateRedirectURIs(tooMany); !errors.Is(err, ErrTooManyRedirectURIs) {
		t.Fatalf("cap: %v", err)
	}
}

func TestVerifierMatches_S256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !verifierMatches(verifier, challenge) {
		t.Fatal("the RFC 7636 example must match")
	}
	if verifierMatches(verifier+"x", challenge) {
		t.Fatal("a wrong verifier must not match")
	}
}
