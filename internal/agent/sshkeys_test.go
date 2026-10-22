package agent

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
)

func oneAccountReport(name string, keys ...sshkeys.Key) sshkeys.Report {
	if keys == nil {
		keys = []sshkeys.Key{}
	}
	return sshkeys.Report{Accounts: []sshkeys.Account{{Name: name, State: sshkeys.StateRead, Keys: keys}}}
}

func TestSSHReports_TwoIdenticalReadingsSendOneSection_AChangeSendsAnother(t *testing.T) {
	reports := &sshReports{}
	same := oneAccountReport("root")

	if !reports.update(same) {
		t.Fatal("le premier relevé doit compter comme neuf")
	}
	if reports.take() == nil {
		t.Fatal("le premier relevé doit partir")
	}
	if reports.update(oneAccountReport("root")) {
		t.Fatal("un relevé identique ne doit pas compter comme un changement")
	}
	if reports.take() != nil {
		t.Fatal("un relevé identique est reparti")
	}
	if !reports.update(oneAccountReport("lucas")) {
		t.Fatal("un changement doit compter")
	}
	if taken := reports.take(); taken == nil || taken.Accounts[0].Name != "lucas" {
		t.Fatalf("le changement n'est pas parti : %+v", taken)
	}
	if reports.take() != nil {
		t.Fatal("le même relevé est parti deux fois")
	}
}

func TestSSHReports_ASignalThatFailedPutsTheReportBack(t *testing.T) {
	reports := &sshReports{}
	reports.update(oneAccountReport("root"))
	sent := reports.take()

	reports.restore(sent)

	if reports.take() == nil {
		t.Fatal("le relevé d'un signal perdu n'est pas reparti")
	}
}

func TestSSHReports_ANewStreamSaysTheReportAgain(t *testing.T) {
	reports := &sshReports{}
	reports.update(oneAccountReport("root"))
	reports.take()

	reports.markReplayed()

	if reports.take() == nil {
		t.Fatal("un flux neuf doit redire le relevé")
	}
}

func TestSSHReports_NothingReadYet_NothingToSend(t *testing.T) {
	reports := &sshReports{}
	reports.markReplayed()
	if reports.take() != nil {
		t.Fatal("un relevé qui n'existe pas est parti")
	}
	reports.restore(nil)
	if reports.take() != nil {
		t.Fatal("restore(nil) a inventé un relevé")
	}
}

// Le tampon du signal : le relevé part avec le premier signal, pas avec le
// suivant, repart sur un flux neuf, et un signal perdu le remet.
func TestSignal_TheSSHSectionGoesOnceThenOnlyAfterAChangeOrANewStream(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var failing atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "", "v", false)
	if err != nil {
		t.Fatal(err)
	}
	waiting := filledPending()
	waiting.readings.take(10)
	send := func() error {
		return signal(context.Background(), client, "session", waiting, slog.New(slog.DiscardHandler))
	}
	carriesSSH := func(index int) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(bodies[index], `"ssh_keys"`)
	}

	if err := send(); err != nil {
		t.Fatal(err)
	}
	if err := send(); err != nil {
		t.Fatal(err)
	}
	waiting.sshKeys.markReplayed()
	if err := send(); err != nil {
		t.Fatal(err)
	}
	failing.Store(true)
	waiting.sshKeys.update(oneAccountReport("lucas"))
	if err := send(); err == nil {
		t.Fatal("une panne du serveur doit remonter")
	}
	failing.Store(false)
	if err := send(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	count := len(bodies)
	mu.Unlock()
	if count != 5 {
		t.Fatalf("%d signaux", count)
	}
	want := []bool{true, false, true, true, true}
	for index, expected := range want {
		if carriesSSH(index) != expected {
			t.Errorf("signal %d : section ssh_keys = %v, attendu %v", index, carriesSSH(index), expected)
		}
	}
}

func TestSSHKeysLoop_ReadsAtStartAndEveryPeriodAndStopsWithTheAgent(t *testing.T) {
	reports := &sshReports{}
	var reads atomic.Int32
	collect := func() (sshkeys.Report, bool) {
		n := reads.Add(1)
		if n == 2 {
			return sshkeys.Report{}, false
		}
		return oneAccountReport("root"), true
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sshKeysLoop(ctx, collect, reports, 5*time.Millisecond, slog.New(slog.DiscardHandler))
		close(done)
	}()

	waitUntil(t, "plusieurs relevés", func() bool { return reads.Load() >= 4 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("la boucle survit à l'agent")
	}

	if taken := reports.take(); taken == nil || taken.Accounts[0].Name != "root" {
		t.Fatalf("relevé : %+v", taken)
	}
	if reports.take() != nil {
		t.Fatal("des relevés identiques sont repartis")
	}
}

// De bout en bout : l'agent relève, envoie, le serveur garde. Les comptes de
// l'hôte ne sont pas lus (banc).
func TestRun_ReportsItsSSHKeysToTheServer(t *testing.T) {
	b := newBench(t)
	token, _, err := b.machines.CreateToken(context.Background(), "vps-nice-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	options := b.options(token)
	options.readSSHKeys = func() (sshkeys.Report, bool) {
		return oneAccountReport("deploy", sshkeys.Key{File: "authorized_keys", Fingerprint: "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8", Type: "ssh-ed25519", Bits: 256, Comment: "ci", Options: []string{"restrict"}}), true
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, options) }()
	online := b.waitFor(t, "vps-nice-1", func(s machine.Status) bool { return s.Online })

	waitUntil(t, "le relevé arrive au serveur", func() bool {
		snapshot, err := b.sshKeys.Machine(context.Background(), online.ID)
		return err == nil && len(snapshot.Accounts) == 1 && len(snapshot.Accounts[0].Keys) == 1
	})

	snapshot, _ := b.sshKeys.Machine(context.Background(), online.ID)
	key := snapshot.Accounts[0].Keys[0]
	if snapshot.Accounts[0].Name != "deploy" || key.Comment != "ci" || len(key.Options) != 1 || key.Options[0] != "restrict" {
		t.Fatalf("relevé : %+v", snapshot)
	}
	cancel()
	<-done
}

// Le relevé de la vraie machine ne plante pas et ne rend que des comptes bornés.
func TestReadSSHKeys_TheRealMachineGivesABoundedReport(t *testing.T) {
	report, ok := readSSHKeys()
	if !ok {
		t.Skip("pas de /etc/passwd ici")
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("le relevé de cette machine est refusé par le serveur : %v", err)
	}
}
