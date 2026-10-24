package action_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
)

// Le secret est signé avec l'ordre, donc l'agent le lit ; il n'est jamais
// écrit : ni dans les paramètres du run, ni dans une ligne du journal
// d'audit, ni dans le journal du service. L'audit dit seulement qu'un secret
// est parti, sous son nom.
func TestSendWithSecret_SignsTheSecretButNeverStoresOrJournalsIt(t *testing.T) {
	var logs bytes.Buffer
	b := newBenchLogging(t, "", slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	const value = "PHRASE=une valeur de test qui ne doit jamais sortir" // gitleaks:allow
	secret, err := json.Marshal(map[string]string{"env": value})
	if err != nil {
		t.Fatal(err)
	}
	key := action.GenerateKey()
	b.service.SetKey(key)

	run, err := b.service.SendWithSecret(operator(), machineID, agentwire.KindAgentPing, json.RawMessage(`{"a":1}`), secret, "env")
	if err != nil {
		t.Fatal(err)
	}

	signed := b.pusher.sent[0].Order
	if !ed25519.Verify(key.PublicKey(), agentwire.OrderSigningBytes(signed.Body), signed.Signature) {
		t.Fatal("the order is not signed")
	}
	var body agentwire.OrderBody
	if err := json.Unmarshal(signed.Body, &body); err != nil || string(body.Secret) != string(secret) || body.RunID != run.ID {
		t.Fatalf("the signed body does not carry the secret: %s %v", signed.Body, err)
	}

	stored := b.runs(t)
	if len(stored) != 1 || strings.Contains(string(stored[0].Params), "PHRASE") || string(stored[0].Params) != `{"a":1}` {
		t.Fatalf("params %s", stored[0].Params)
	}
	events, err := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 50})
	if err != nil || len(events) == 0 {
		t.Fatalf("events %v %v", events, err)
	}
	for _, event := range events {
		for name, detail := range event.Details {
			if strings.Contains(detail, "PHRASE") || strings.Contains(detail, "valeur") || strings.Contains(name, "PHRASE") {
				t.Errorf("the secret reached the audit: %s=%s", name, detail)
			}
		}
	}
	if events[0].Action != audit.ActionOrder || events[0].Details["env"] != "sent" {
		t.Errorf("the audit must say a secret left: %+v", events[0])
	}
	if strings.Contains(logs.String(), "PHRASE") || strings.Contains(logs.String(), "valeur") {
		t.Errorf("the secret reached the log: %s", logs.String())
	}
}

// Send reste Send : sans secret, l'enveloppe ne porte pas le champ.
func TestSend_CarriesNoSecret(t *testing.T) {
	b := newBench(t)
	if _, err := b.service.Ping(operator(), machineID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b.pusher.sent[0].Order.Body), "secret") {
		t.Errorf("body %s", b.pusher.sent[0].Order.Body)
	}
}

func TestSendWithSecret_RefusesMoreThanAnAgentReads(t *testing.T) {
	b := newBench(t)
	huge := json.RawMessage(`"` + strings.Repeat("x", agentwire.MaxSecretBytes) + `"`)
	_, err := b.service.SendWithSecret(operator(), machineID, agentwire.KindAgentPing, nil, huge, "env")
	if !errors.Is(err, action.ErrSecretTooLarge) {
		t.Fatalf("got %v", err)
	}
	if runs := b.runs(t); len(runs) != 0 {
		t.Fatalf("a run was written: %+v", runs)
	}
}

// Un ordre accepté qui ne finit pas échoue au bout d'une heure, raison
// timeout ; un ordre fini ou plus récent n'est pas touché, et une réponse
// tardive ne défait pas l'échec.
func TestTimeout_FailsAnAcceptedRunAfterAnHour(t *testing.T) {
	b := newBenchReporting(t, "backup.receive=/srv/backups")
	ctx := operator()
	long, err := b.service.Send(ctx, machineID, agentwire.KindBackupReceive, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.service.Send(ctx, machineID, agentwire.KindBackupPrune, nil)
	if err != nil {
		t.Fatal(err)
	}
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{
		{RunID: long.ID, State: agentwire.ActionAccepted},
		{RunID: other.ID, State: agentwire.ActionSucceeded},
	}}
	if err := b.service.Record(context.Background(), machineID, report); err != nil {
		t.Fatal(err)
	}
	b.now = benchNow.Add(action.MaxRunDuration)
	if err := b.service.Timeout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if states := b.states(t); states[long.ID] != action.StateAccepted {
		t.Fatalf("exactly an hour is not past it: %+v", states)
	}
	b.now = benchNow.Add(action.MaxRunDuration + time.Second)
	if err := b.service.Timeout(context.Background()); err != nil {
		t.Fatal(err)
	}
	states := b.states(t)
	if states[long.ID] != action.StateFailed || states[other.ID] != action.StateSucceeded {
		t.Fatalf("states %+v", states)
	}
	for _, run := range b.runs(t) {
		if run.ID == long.ID && (run.Reason != action.ReasonTimeout || run.FinishedAt.IsZero()) {
			t.Fatalf("run %+v", run)
		}
	}
	late := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: long.ID, State: agentwire.ActionSucceeded}}}
	if err := b.service.Record(context.Background(), machineID, late); err != nil {
		t.Fatal(err)
	}
	if states := b.states(t); states[long.ID] != action.StateFailed {
		t.Fatalf("a late answer undid the timeout: %+v", states)
	}
}

// Les ordres qui écrivent exigent leur droit : le serveur ne les envoie pas
// à un agent qui a dit ne pas l'avoir.
func TestSend_BackupOrdersNeedTheBackupRight(t *testing.T) {
	b := newBenchReporting(t, "observe")
	for _, kind := range []agentwire.Kind{agentwire.KindBackupReceive, agentwire.KindBackupPrune} {
		if _, err := b.service.Send(operator(), machineID, kind, nil); !errors.Is(err, action.ErrRightMissing) {
			t.Errorf("%s: %v", kind, err)
		}
	}
	if right, ok := action.RightOf(agentwire.KindBackupReceive); !ok || right != "backup.receive" {
		t.Errorf("right %q %t", right, ok)
	}
}

// backup.produce exige son droit, et le producteur que l'ordre nomme : un agent
// qui n'a pas le droit, ou qui l'a pour un autre producteur, ne reçoit pas
// l'ordre. Des paramètres illisibles passent : l'agent les refusera (bad_order).
func TestSend_ProduceNeedsTheRightAndItsProducer(t *testing.T) {
	if right, ok := action.RightOf(agentwire.KindBackupProduce); !ok || right != "backup.produce" {
		t.Fatalf("right %q %t", right, ok)
	}
	produce := func(producer string) []byte {
		params, err := json.Marshal(agentwire.BackupProduceParams{Producer: producer, Target: "5012", Prefix: "pve-vm-5012", Recipient: "age1x", Parcel: "colis"})
		if err != nil {
			t.Fatal(err)
		}
		return params
	}
	without := newBenchReporting(t, "observe")
	if _, err := without.service.Send(operator(), machineID, agentwire.KindBackupProduce, produce("proxmox-vzdump")); !errors.Is(err, action.ErrRightMissing) {
		t.Errorf("without the right: %v", err)
	}
	with := newBenchReporting(t, "observe,backup.produce=proxmox-vzdump")
	if _, err := with.service.Send(operator(), machineID, agentwire.KindBackupProduce, produce("proxmox-vzdump")); err != nil {
		t.Errorf("with the right: %v", err)
	}
	for name, params := range map[string][]byte{"another producer": produce("test-echo"), "no producer": produce("")} {
		if _, err := with.service.Send(operator(), machineID, agentwire.KindBackupProduce, params); !errors.Is(err, action.ErrRightMissing) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(with.pusher.sent) != 1 {
		t.Errorf("%d orders left, want only the covered one", len(with.pusher.sent))
	}
	if _, err := with.service.Send(operator(), machineID, agentwire.KindBackupProduce, []byte("pas du json")); err != nil {
		t.Errorf("unreadable params are the agent's to refuse: %v", err)
	}
}
