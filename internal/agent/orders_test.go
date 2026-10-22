package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

const (
	orderMachineID = "11111111-2222-4333-8444-555555555555"
	orderAgentID   = "agt_0000000000000001"
)

var orderNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// orderBench est un juge d'ordres sur une identité écrite dans un dossier
// d'état, et la clé du serveur qu'elle a épinglée.
type orderBench struct {
	runner   *orderRunner
	results  *actionResults
	stateDir *os.Root
	private  ed25519.PrivateKey
}

func newOrderBench(t *testing.T) *orderBench {
	t.Helper()
	return newOrderBenchWithRights(t, nil)
}

// newOrderBenchWithRights écrit une identité qui porte ces droits.
func newOrderBenchWithRights(t *testing.T, rights []string) *orderBench {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	stateDir, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stateDir.Close() })
	identity := Identity{MachineID: orderMachineID, AgentID: orderAgentID, ServerKey: public, Rights: rights}
	if err := SaveIdentity(stateDir, identity); err != nil {
		t.Fatal(err)
	}
	results := newActionResults()
	runner := newOrderRunner(stateDir, identity, "v0.0.1", results, slog.New(slog.DiscardHandler))
	runner.now = func() time.Time { return orderNow }
	return &orderBench{runner: runner, results: results, stateDir: stateDir, private: private}
}

// order rend un ordre ping valide de rang seq, à retoucher par le test.
func order(seq int64) agentwire.OrderBody {
	return agentwire.OrderBody{
		RunID: fmt.Sprintf("run-%d", seq), MachineID: orderMachineID, AgentID: orderAgentID, Kind: agentwire.KindAgentPing,
		NotAfter: orderNow.Add(time.Minute).Unix(), Seq: seq,
	}
}

func (b *orderBench) send(t *testing.T, body agentwire.OrderBody, signer ed25519.PrivateKey) agentwire.ActionResult {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	signed := agentwire.SignedOrder{Body: encoded, Signature: ed25519.Sign(signer, agentwire.OrderSigningBytes(encoded))}
	data, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	b.runner.receive(data)
	report := b.results.take()
	if report == nil || len(report.Results) != 1 {
		t.Fatalf("one result expected, got %+v", report)
	}
	return report.Results[0]
}

// Un ordre signé par la clé épinglée, pour cette machine et cet agent, à
// temps et au rang suivant, s'exécute ; son rang est écrit avant.
func TestOrderRunner_ExecutesAValidPingAndKeepsItsSeq(t *testing.T) {
	b := newOrderBench(t)
	result := b.send(t, order(1), b.private)
	if result.State != agentwire.ActionSucceeded || result.RunID != "run-1" || len(result.Output) != 1 || result.Output[0].Line != "pong v0.0.1" {
		t.Fatalf("result %+v", result)
	}
	select {
	case <-b.results.ready:
	default:
		t.Fatal("the stream loop was not woken")
	}
	saved, err := LoadIdentity(b.stateDir)
	if err != nil || saved.LastOrderSeq != 1 {
		t.Fatalf("saved seq %d %v", saved.LastOrderSeq, err)
	}
}

// Chaque garde refuse, avec sa raison, et le refus se rapporte au run.
func TestOrderRunner_RefusesAndReportsEachBadOrder(t *testing.T) {
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*agentwire.OrderBody)
		signer func(b *orderBench) ed25519.PrivateKey
		reason string
	}{
		{"signed by another key", nil, func(*orderBench) ed25519.PrivateKey { return stranger }, agentwire.ReasonBadSignature},
		{"another machine", func(o *agentwire.OrderBody) { o.MachineID = "99999999-2222-4333-8444-555555555555" }, nil, agentwire.ReasonWrongMachine},
		{"another agent", func(o *agentwire.OrderBody) { o.AgentID = "agt_0000000000000002" }, nil, agentwire.ReasonWrongAgent},
		{"past its deadline", func(o *agentwire.OrderBody) { o.NotAfter = orderNow.Unix() }, nil, agentwire.ReasonExpired},
		{"replayed", func(o *agentwire.OrderBody) { o.Seq = 0 }, nil, agentwire.ReasonReplayed},
		{"unknown kind", func(o *agentwire.OrderBody) { o.Kind = "shell.exec" }, nil, agentwire.ReasonUnknownKind},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newOrderBench(t)
			body := order(1)
			if c.change != nil {
				c.change(&body)
			}
			signer := b.private
			if c.signer != nil {
				signer = c.signer(b)
			}
			result := b.send(t, body, signer)
			if result.State != agentwire.ActionRefused || result.Reason != c.reason || result.RunID != body.RunID {
				t.Fatalf("result %+v, want refused %s", result, c.reason)
			}
			if saved, _ := LoadIdentity(b.stateDir); saved.LastOrderSeq != 0 {
				t.Fatalf("a refused order moved the seq to %d", saved.LastOrderSeq)
			}
		})
	}
}

// Le même ordre rejoué, même signé, est refusé : le rang ne recule pas, et
// survit au redémarrage de l'agent.
func TestOrderRunner_RefusesAReplayEvenAfterARestart(t *testing.T) {
	b := newOrderBench(t)
	if result := b.send(t, order(3), b.private); result.State != agentwire.ActionSucceeded {
		t.Fatalf("first %+v", result)
	}
	if result := b.send(t, order(3), b.private); result.Reason != agentwire.ReasonReplayed {
		t.Fatalf("replay %+v", result)
	}
	reloaded, err := LoadIdentity(b.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	b.runner = newOrderRunner(b.stateDir, reloaded, "v0.0.1", b.results, slog.New(slog.DiscardHandler))
	b.runner.now = func() time.Time { return orderNow }
	if result := b.send(t, order(2), b.private); result.Reason != agentwire.ReasonReplayed {
		t.Fatalf("after restart %+v", result)
	}
}

// Sans clé épinglée, aucun ordre ne passe ; un corps illisible ne donne pas
// de run à qui répondre.
func TestOrderRunner_WithoutAPinnedKeyRefusesEverything(t *testing.T) {
	b := newOrderBench(t)
	b.runner.identity.ServerKey = nil
	if result := b.send(t, order(1), b.private); result.Reason != agentwire.ReasonServerKeyMissing {
		t.Fatalf("result %+v", result)
	}
	b.runner.receive([]byte("pas du json"))
	if report := b.results.take(); report != nil {
		t.Fatalf("an unreadable envelope was reported: %+v", report)
	}
}

// L'agent applique les droits de son fichier : un ordre dont le droit
// manque est refusé avec ce droit, sans prendre son rang ; avec le droit,
// agent.update est jugé par l'agent : celui du test ne tourne pas depuis son
// dossier bin/.
func TestOrderRunner_AppliesTheRightsOfItsFile(t *testing.T) {
	b := newOrderBench(t)
	update := order(1)
	update.Kind = agentwire.KindAgentUpdate
	result := b.send(t, update, b.private)
	if result.State != agentwire.ActionRefused || result.Reason != "right_missing:agent.update" {
		t.Fatalf("without the right: %+v", result)
	}
	if saved, _ := LoadIdentity(b.stateDir); saved.LastOrderSeq != 0 {
		t.Fatalf("a refused order took its seq: %d", saved.LastOrderSeq)
	}
	if result := b.send(t, order(1), b.private); result.State != agentwire.ActionSucceeded {
		t.Fatalf("ping needs observe only: %+v", result)
	}

	granted := newOrderBenchWithRights(t, []string{"observe", "agent.update"})
	result = granted.send(t, update, granted.private)
	if result.State != agentwire.ActionRefused || result.Reason != agentwire.ReasonNotSelfUpdatable {
		t.Fatalf("with the right: %+v", result)
	}
	// Un fichier illisible vaut observe seul, le plus étroit.
	unreadable := newOrderBenchWithRights(t, []string{"agent.update=/nope"})
	if result := unreadable.send(t, update, unreadable.private); result.Reason != "right_missing:agent.update" {
		t.Fatalf("unreadable rights: %+v", result)
	}
}

// agent.server_key, signé par la clé épinglée, la remplace par la neuve : sur
// disque comme en mémoire. Les ordres suivants ne se vérifient qu'avec la
// neuve ; une clé mal formée ne remplace rien.
func TestOrderRunner_PinsTheNewServerKey(t *testing.T) {
	b := newOrderBench(t)
	newPublic, newPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rekey := func(seq int64, key []byte) agentwire.OrderBody {
		body := order(seq)
		body.Kind = agentwire.KindServerKey
		body.Params, _ = json.Marshal(agentwire.ServerKeyParams{PublicKey: base64.StdEncoding.EncodeToString(key)})
		return body
	}
	if result := b.send(t, rekey(1, []byte("short")), b.private); result.State != agentwire.ActionFailed || result.Reason != agentwire.ReasonBadOrder {
		t.Fatalf("a malformed key: %+v", result)
	}
	if result := b.send(t, rekey(2, newPublic), b.private); result.State != agentwire.ActionSucceeded {
		t.Fatalf("rekey %+v", result)
	}
	saved, err := LoadIdentity(b.stateDir)
	if err != nil || !newPublic.Equal(ed25519.PublicKey(saved.ServerKey)) || !newPublic.Equal(ed25519.PublicKey(b.runner.serverKey())) {
		t.Fatalf("the new key was not pinned: %v", err)
	}
	if result := b.send(t, order(3), b.private); result.Reason != agentwire.ReasonBadSignature {
		t.Fatalf("the previous key still signs: %+v", result)
	}
	if result := b.send(t, order(4), newPrivate); result.State != agentwire.ActionSucceeded {
		t.Fatalf("the new key does not sign: %+v", result)
	}
}
