package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agent"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// failingReader échoue le test si l'entrée est lue.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Helper()
	r.t.Fatal("l'entrée standard ne devait pas être lue")
	return 0, io.EOF
}

func testCatalog(t *testing.T) lang.Catalog {
	t.Helper()
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	return catalogs.For(lang.French)
}

func emptyStateDir(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestEnrollmentTokenFor_ReadsTheTokenOnStandardInput(t *testing.T) {
	var prompt bytes.Buffer
	text := testCatalog(t)

	token, err := enrollmentTokenFor(emptyStateDir(t), "https://opencloud.example.org", strings.NewReader("  oc_abc234 \r\nautre ligne\n"), &prompt, false, text)
	if err != nil || token != "oc_abc234" {
		t.Fatalf("token %q, err %v", token, err)
	}
	if prompt.Len() != 0 {
		t.Fatalf("a pipe gets no prompt: %q", prompt.String())
	}

	// Sans retour à la ligne final, la ligne vaut quand même.
	token, err = enrollmentTokenFor(emptyStateDir(t), "https://opencloud.example.org", strings.NewReader("oc_abc234"), &prompt, false, text)
	if err != nil || token != "oc_abc234" {
		t.Fatalf("token %q, err %v", token, err)
	}

	// Sur un terminal, l'invite dit quoi saisir.
	token, err = enrollmentTokenFor(emptyStateDir(t), "https://opencloud.example.org", strings.NewReader("oc_abc234\n"), &prompt, true, text)
	if err != nil || token != "oc_abc234" || prompt.String() != text.Get("agent.token_prompt") {
		t.Fatalf("token %q, err %v, prompt %q", token, err, prompt.String())
	}
}

func TestEnrollmentTokenFor_RefusesAnEmptyOrClosedInput(t *testing.T) {
	text := testCatalog(t)
	for name, input := range map[string]string{"fermée": "", "ligne vide": "\n", "espaces": "   \t \n"} {
		t.Run(name, func(t *testing.T) {
			_, err := enrollmentTokenFor(emptyStateDir(t), "https://opencloud.example.org", strings.NewReader(input), io.Discard, false, text)
			if !errors.Is(err, agent.ErrTokenMissing) {
				t.Fatalf("got %v", err)
			}
			if message, refused := agent.Explain(err, text); !refused || message != text.Get("agent.token_missing") {
				t.Fatalf("explained %q %v", message, refused)
			}
		})
	}
}

func TestEnrollmentTokenFor_NeverReadsInputOfAnEnrolledAgent(t *testing.T) {
	root := emptyStateDir(t)
	identity, err := agent.NewIdentity("https://opencloud.example.org", "", lang.French, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveIdentity(root, identity); err != nil {
		t.Fatal(err)
	}
	// Relance sous systemd : identité présente, avec ou sans -server.
	for _, server := range []string{"", "https://opencloud.example.org"} {
		token, err := enrollmentTokenFor(root, server, failingReader{t}, io.Discard, true, testCatalog(t))
		if err != nil || token != "" {
			t.Fatalf("server %q: token %q, err %v", server, token, err)
		}
	}
}

func TestEnrollmentTokenFor_WithoutServerLeavesTheRefusalToTheAgent(t *testing.T) {
	token, err := enrollmentTokenFor(emptyStateDir(t), "", failingReader{t}, io.Discard, true, testCatalog(t))
	if err != nil || token != "" {
		t.Fatalf("token %q, err %v", token, err)
	}
}

func TestRunAgent_RefusesTheTokenFlag(t *testing.T) {
	stateDir := t.TempDir()
	for _, args := range [][]string{
		{"-server", "https://opencloud.example.org", "-token", "oc_fromflag"},
		{"-token=", "-server", "https://opencloud.example.org"},
	} {
		err := runAgent(append(args, "-state", stateDir))
		var refused *refusal
		if !errors.As(err, &refused) || refused.message != testCatalog(t).Get("agent.token_flag_refused") {
			t.Fatalf("%v: got %v, want the -token refusal", args, err)
		}
		if strings.Contains(refused.message, "oc_fromflag") {
			t.Fatal("the refusal must not repeat the token")
		}
	}
	if entries, err := os.ReadDir(stateDir); err != nil || len(entries) != 0 {
		t.Fatalf("a refused start must write nothing: %v, %v", entries, err)
	}
}

// endlessReader est un flux sans fin et sans retour à la ligne ; il compte
// ce qu'on lui a pris.
type endlessReader struct{ read int }

func (r *endlessReader) Read(buffer []byte) (int, error) {
	for i := range buffer {
		buffer[i] = 'a'
	}
	r.read += len(buffer)
	return len(buffer), nil
}

func TestReadEnrollmentToken_StopsAtTheBound(t *testing.T) {
	in := &endlessReader{}
	token, err := readEnrollmentToken(in)
	if err != nil || len(token) != maxTokenLine || in.read > maxTokenLine {
		t.Fatalf("token of %d bytes, %d bytes read, err %v", len(token), in.read, err)
	}
}
