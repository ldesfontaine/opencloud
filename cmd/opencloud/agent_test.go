package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
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
	var logs, prompt bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	text := testCatalog(t)

	token, err := enrollmentTokenFor(emptyStateDir(t), "https://opencloud.example.org", "", strings.NewReader("  oc_abc234 \r\nautre ligne\n"), &prompt, false, text, logger)
	if err != nil || token != "oc_abc234" {
		t.Fatalf("token %q, err %v", token, err)
	}
	if prompt.Len() != 0 || logs.Len() != 0 {
		t.Fatalf("a pipe gets neither prompt nor warning: %q %q", prompt.String(), logs.String())
	}

	// Sans retour à la ligne final, la ligne vaut quand même.
	token, err = enrollmentTokenFor(emptyStateDir(t), "https://opencloud.example.org", "", strings.NewReader("oc_abc234"), &prompt, false, text, logger)
	if err != nil || token != "oc_abc234" {
		t.Fatalf("token %q, err %v", token, err)
	}

	// Sur un terminal, l'invite dit quoi saisir.
	token, err = enrollmentTokenFor(emptyStateDir(t), "https://opencloud.example.org", "", strings.NewReader("oc_abc234\n"), &prompt, true, text, logger)
	if err != nil || token != "oc_abc234" || prompt.String() != text.Get("agent.token_prompt") {
		t.Fatalf("token %q, err %v, prompt %q", token, err, prompt.String())
	}
}

func TestEnrollmentTokenFor_RefusesAnEmptyOrClosedInput(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	text := testCatalog(t)
	for name, input := range map[string]string{"fermée": "", "ligne vide": "\n", "espaces": "   \t \n"} {
		t.Run(name, func(t *testing.T) {
			_, err := enrollmentTokenFor(emptyStateDir(t), "https://opencloud.example.org", "", strings.NewReader(input), io.Discard, false, text, logger)
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
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	root := emptyStateDir(t)
	identity, err := agent.NewIdentity("https://opencloud.example.org", "", lang.French)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveIdentity(root, identity); err != nil {
		t.Fatal(err)
	}
	// Relance sous systemd : identité présente, avec ou sans -server.
	for _, server := range []string{"", "https://opencloud.example.org"} {
		token, err := enrollmentTokenFor(root, server, "", failingReader{t}, io.Discard, true, testCatalog(t), logger)
		if err != nil || token != "" {
			t.Fatalf("server %q: token %q, err %v", server, token, err)
		}
	}
}

func TestEnrollmentTokenFor_WithoutServerLeavesTheRefusalToTheAgent(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	token, err := enrollmentTokenFor(emptyStateDir(t), "", "", failingReader{t}, io.Discard, true, testCatalog(t), logger)
	if err != nil || token != "" {
		t.Fatalf("token %q, err %v", token, err)
	}
}

func TestEnrollmentTokenFor_KeepsTheDeprecatedFlagAndWarns(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	text := testCatalog(t)
	token, err := enrollmentTokenFor(emptyStateDir(t), "https://opencloud.example.org", "oc_fromflag", failingReader{t}, io.Discard, true, text, logger)
	if err != nil || token != "oc_fromflag" {
		t.Fatalf("token %q, err %v", token, err)
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), "ps") {
		t.Fatalf("the flag must be warned about: %q", logs.String())
	}
	if strings.Contains(logs.String(), "oc_fromflag") {
		t.Fatal("the warning must not repeat the token")
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
