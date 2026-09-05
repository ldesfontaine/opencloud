package cli

import (
	"bytes"
	"errors"
	"testing"
)

func TestRun_Version_PrintsVersion(t *testing.T) {
	var out bytes.Buffer

	if err := Run([]string{"version"}, "1.2.3", &out); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if got, want := out.String(), "opencloud 1.2.3\n"; got != want {
		t.Fatalf("sortie = %q, attendu %q", got, want)
	}
}

func TestRun_UnknownCommand_ReturnsError(t *testing.T) {
	var out bytes.Buffer

	err := Run([]string{"explode"}, "dev", &out)
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("erreur = %v, attendu ErrUnknownCommand", err)
	}
}
