package agentwire

import (
	"errors"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Les noms d'événement d'observation sont ceux que les agents lisent ; un
// ordre part sous « order », quel que soit son genre.
func TestCommand_EncodesUnderItsFrozenEventName(t *testing.T) {
	cases := []struct {
		command Command
		event   string
		data    string
	}{
		{LogsCommand(container.LogRequest{ID: "r", ContainerID: "c", Tail: 10}), "logs", `{"id":"r","container_id":"c","tail":10,"follow":false}`},
		{StopLogsCommand("r"), "logs_stop", `{"id":"r","container_id":"","tail":0,"follow":false}`},
		{ProbesCommand(probe.Assignment{}), "probes", `{"probes":null}`},
		{ImagesCommand(update.Assignment{Now: true}), "image_checks", `{"now":true}`},
		{OrderCommand(KindAgentPing, SignedOrder{Key: "k"}), "order", `{"body":null,"signature":null,"key":"k"}`},
	}
	for _, c := range cases {
		event, data, err := c.command.Encode()
		if err != nil || event != c.event || string(data) != c.data {
			t.Errorf("%s: %q %s %v", c.command.Kind, event, data, err)
		}
	}
}

// La liste est fermée : un genre inconnu ou un contenu absent ne s'encode
// pas, un événement inconnu ne se lit pas, et un ordre ne se lit jamais
// comme une observation.
func TestCommand_TheListIsClosed(t *testing.T) {
	if _, _, err := (Command{Kind: "shell.exec"}).Encode(); !errors.Is(err, ErrUnknownCommand) {
		t.Errorf("unknown kind: %v", err)
	}
	if _, _, err := (Command{Kind: KindProbes}).Encode(); !errors.Is(err, ErrEmptyCommand) {
		t.Errorf("no content: %v", err)
	}
	if _, _, err := (Command{Kind: KindAgentPing}).Encode(); !errors.Is(err, ErrEmptyCommand) {
		t.Errorf("order without envelope: %v", err)
	}
	for _, event := range []string{"shell", EventOrder, string(KindAgentPing)} {
		if _, err := DecodeObservation(event, []byte("{}")); !errors.Is(err, ErrUnknownCommand) {
			t.Errorf("%s: %v", event, err)
		}
	}
	decoded, err := DecodeObservation("image_checks", []byte(`{"excluded":["a"]}`))
	if err != nil || decoded.Kind != KindImages || decoded.Images == nil || decoded.Images.Excluded[0] != "a" {
		t.Errorf("decoded %+v %v", decoded, err)
	}
	if !KindAgentPing.IsOrder() || KindLogs.IsOrder() {
		t.Error("IsOrder")
	}
}

// L'empreinte est celle d'OpenSSH : « SHA256: » et 43 caractères.
func TestKeyFingerprint_HasTheOpenSSHForm(t *testing.T) {
	got := KeyFingerprint(make([]byte, 32))
	if got != "SHA256:Zmh6rfhivXdsj8GLjp+OIAiXFIVu4jOzkCpZHQ1fKSU" {
		t.Fatalf("got %s", got)
	}
}
