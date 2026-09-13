package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

func TestExplain_SpeaksTheOperatorLanguageAndTellsRefusalsApart(t *testing.T) {
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	french, english := catalogs.For(lang.French), catalogs.For(lang.English)

	message, refused := Explain(&ServerError{Status: 409, Code: "token_consumed"}, french)
	if !refused || message != french.Get("agent.token_consumed") {
		t.Fatalf("fr consumed: %q %v", message, refused)
	}
	message, _ = Explain(&ServerError{Status: 409, Code: "token_consumed"}, english)
	if message != english.Get("agent.token_consumed") || message == french.Get("agent.token_consumed") {
		t.Fatalf("en consumed: %q", message)
	}
	if message, refused := Explain(fmt.Errorf("%w: %w", ErrIdentityRefused, errors.New("x")), french); !refused || message != french.Get("agent.identity_refused") {
		t.Fatalf("identity: %q %v", message, refused)
	}
	if _, refused := Explain(ErrNotEnrolled, french); !refused {
		t.Fatal("not enrolled must be a refusal")
	}
	message, refused = Explain(errors.New("dial tcp: connection refused"), french)
	if refused || message == "" || message == "dial tcp: connection refused" {
		t.Fatalf("plain error: %q %v", message, refused)
	}
	if _, refused := Explain(&ServerError{Status: 500, Code: "internal"}, french); refused {
		t.Fatal("a server failure is not a refusal")
	}
}
