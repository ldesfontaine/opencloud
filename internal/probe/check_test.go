package probe

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

var checkNow = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

// check sonde avec le magasin du système seul : une machine sans autorité
// interne ajoutée, le cas de presque toutes.
func check(task Task, now time.Time) Result {
	return NewChecker(nil).Check(context.Background(), task, now)
}

func httpTask(target string) Task {
	return Task{ID: "p1", Kind: KindHTTP, Target: target, IntervalSeconds: 60, TimeoutSeconds: 5, Method: "GET", ExpectedStatus: "2xx"}
}

func TestCheckHTTP_AcceptsTheExpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	result := check(httpTask(server.URL), checkNow)
	if result.Outcome != OutcomeUp || result.Reason != ReasonNone {
		t.Fatalf("attendu en ligne, obtenu %+v", result)
	}
	if result.Code == nil || *result.Code != http.StatusNoContent {
		t.Fatalf("le code reçu n'est pas rendu: %+v", result.Code)
	}
}

func TestCheckHTTP_RefusesAnUnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	result := check(httpTask(server.URL), checkNow)
	if result.Outcome != OutcomeDown || result.Reason != ReasonStatus {
		t.Fatalf("attendu hors ligne pour code inattendu, obtenu %+v", result)
	}
}

func TestCheckHTTP_LooksForTheExpectedText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	task := httpTask(server.URL)
	task.ExpectedBody = `"status":"ok"`
	if result := check(task, checkNow); result.Outcome != OutcomeUp {
		t.Fatalf("texte attendu présent mais refusé: %+v", result)
	}
	task.ExpectedBody = "hors sujet"
	result := check(task, checkNow)
	if result.Outcome != OutcomeDown || result.Reason != ReasonBody {
		t.Fatalf("texte attendu absent mais accepté: %+v", result)
	}
}

// Sans suivre les redirections, une redirection vers une page d'erreur ne
// passe pas pour un succès.
func TestCheckHTTP_RedirectCountsAsTheStatusItIs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ailleurs", http.StatusFound)
	}))
	defer server.Close()

	result := check(httpTask(server.URL), checkNow)
	if result.Outcome != OutcomeDown || result.Reason != ReasonStatus {
		t.Fatalf("la redirection non suivie devait rester un code inattendu: %+v", result)
	}
}

func TestCheckHTTP_StopsAfterTooManyRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/encore", http.StatusFound)
	}))
	defer server.Close()

	task := httpTask(server.URL)
	task.FollowRedirects = true
	result := check(task, checkNow)
	if result.Outcome != OutcomeDown || result.Reason != ReasonRedirect {
		t.Fatalf("attendu trop de redirections, obtenu %+v", result)
	}
}

func TestCheckHTTP_GivesUpAfterTheTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	task := httpTask(server.URL)
	task.TimeoutSeconds = 1
	result := check(task, checkNow)
	if result.Outcome != OutcomeDown || result.Reason != ReasonTimeout {
		t.Fatalf("attendu un délai dépassé, obtenu %+v", result)
	}
}

// Le cœur de la feature : un certificat refusé n'est pas une panne. L'hôte
// répond, l'essai est dégradé, et la requête n'est JAMAIS rejouée vers un
// pair dont on refuse la chaîne.
func TestCheckHTTP_UntrustedCertificateIsDegradedAndNeverReplays(t *testing.T) {
	var served atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	// Le refus de la chaîne est ce que le test prouve : son trace côté
	// serveur n'a rien à faire dans la sortie.
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()

	result := check(httpTask(server.URL), checkNow)
	if result.Outcome != OutcomeDegraded {
		t.Fatalf("attendu dégradé, obtenu %+v", result)
	}
	if result.Reason != ReasonTLSUntrusted {
		t.Fatalf("attendu un certificat non reconnu, obtenu %q", result.Reason)
	}
	if served.Load() != 0 {
		t.Fatalf("la requête a atteint la cible %d fois : elle ne doit jamais être rejouée", served.Load())
	}
	if result.Certificate == nil || result.Certificate.Fingerprint == "" {
		t.Fatalf("la poignée de main nue n'a pas rendu la chaîne vue: %+v", result.Certificate)
	}
}

// Un essai dégradé reste un succès : c'est ce qui laisse l'uptime intact.
func TestOutcome_DegradedIsASuccess(t *testing.T) {
	if !OutcomeDegraded.IsSuccess() || !OutcomeUp.IsSuccess() || OutcomeDown.IsSuccess() {
		t.Fatal("dégradé doit compter comme un succès, hors ligne non")
	}
}

// Un hôte qui ne répond pas du tout est hors ligne, pas dégradé : la
// poignée de main nue est ce qui fait la différence.
func TestCheckHTTP_UnreachableTLSHostIsDown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := "https://" + listener.Addr().String() + "/"
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	result := check(httpTask(target), checkNow)
	if result.Outcome != OutcomeDown {
		t.Fatalf("attendu hors ligne, obtenu %+v", result)
	}
}

// L'adresse de métadonnées des hébergeurs ne se compose jamais, même si
// une sonde plus vieille la porte encore.
func TestCheck_RefusesALinkLocalAddressWithoutDialing(t *testing.T) {
	result := check(httpTask("http://169.254.169.254/latest/meta-data/"), checkNow)
	if result.Outcome != OutcomeDown || result.Reason != ReasonAddress {
		t.Fatalf("attendu une adresse refusée, obtenu %+v", result)
	}
}

func TestCheckTCP_ConnectsAndCloses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	task := Task{ID: "p2", Kind: KindTCP, Target: listener.Addr().String(), IntervalSeconds: 60, TimeoutSeconds: 5}
	if result := check(task, checkNow); result.Outcome != OutcomeUp {
		t.Fatalf("attendu en ligne, obtenu %+v", result)
	}
}

func TestCheckTCP_ReportsARefusedConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	task := Task{ID: "p2", Kind: KindTCP, Target: address, IntervalSeconds: 60, TimeoutSeconds: 5}
	result := check(task, checkNow)
	if result.Outcome != OutcomeDown || result.Reason != ReasonRefused {
		t.Fatalf("attendu une connexion refusée, obtenu %+v", result)
	}
}

func TestCheck_DatesTheResultWithTheClockItIsGiven(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	if got := check(httpTask(server.URL), checkNow).CheckedAt; !got.Equal(checkNow) {
		t.Fatalf("attendu %s, obtenu %s", checkNow, got)
	}
}
