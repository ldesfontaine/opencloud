package probe

import (
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// listenLoopback ouvre un écouteur TCP sur la boucle locale pour les sondes
// qui visent « le réseau local » sans rien servir.
func listenLoopback(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return listener.Addr().String()
}

func tcpTask(target string) Task {
	return Task{ID: "p1", Kind: KindTCP, Target: target, IntervalSeconds: 60, TimeoutSeconds: 2}
}

// Sans probe.lan, la sonde HTTP vers la boucle locale tombe avec sa raison ;
// avec, elle répond.
func TestCheck_LANRefusedWithoutTheRight(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()

	closed := NewChecker(nil, false).Check(t.Context(), httpTask(server.URL), checkNow)
	if closed.Outcome != OutcomeDown || closed.Reason != ReasonLAN {
		t.Fatalf("sans le droit: %+v", closed)
	}
	if closed.Code != nil {
		t.Fatalf("la sonde ne doit rien avoir reçu: %v", *closed.Code)
	}
	open := NewChecker(nil, true).Check(t.Context(), httpTask(server.URL), checkNow)
	if open.Outcome != OutcomeUp {
		t.Fatalf("avec le droit: %+v", open)
	}
}

// Chaque forme d'adresse locale est refusée avant tout appel réseau, en HTTP
// comme en TCP : boucle IPv6, privées, 100.64/10 (Tailscale), ULA, la forme
// IPv4 écrite en IPv6, l'adresse non spécifiée.
func TestCheck_LANRefusedForEveryLocalForm(t *testing.T) {
	hosts := []string{"127.0.0.1", "[::1]", "10.0.0.5", "172.16.3.4", "192.168.1.20", "100.64.0.1", "100.127.255.254", "[fd00::1]", "[::ffff:10.0.0.1]", "[::ffff:127.0.0.1]", "0.0.0.0", "[::]"}
	checker := NewChecker(nil, false)
	for _, host := range hosts {
		for _, task := range []Task{httpTask("http://" + host + ":8080/"), tcpTask(host + ":8080"), {ID: "p1", Kind: KindTCP, Target: host + ":8443", TLS: true, TimeoutSeconds: 2}} {
			result := checker.Check(t.Context(), task, checkNow)
			if result.Outcome != OutcomeDown || result.Reason != ReasonLAN {
				t.Errorf("%s %s tls=%v: %+v", task.Kind, host, task.TLS, result)
			}
		}
	}
}

// Un nom qui résout vers la boucle locale est jugé sur ce qu'il résout.
func TestCheck_LANRefusedForANameThatResolvesLocally(t *testing.T) {
	address := listenLoopback(t)
	_, port, _ := net.SplitHostPort(address)
	name := net.JoinHostPort("localhost", port)

	if result := NewChecker(nil, false).Check(t.Context(), tcpTask(name), checkNow); result.Reason != ReasonLAN {
		t.Fatalf("sans le droit: %+v", result)
	}
	if result := NewChecker(nil, true).Check(t.Context(), tcpTask(name), checkNow); result.Outcome != OutcomeUp {
		t.Fatalf("avec le droit: %+v", result)
	}
	if result := NewChecker(nil, false).Check(t.Context(), httpTask("http://"+name+"/"), checkNow); result.Reason != ReasonLAN {
		t.Fatalf("HTTP sans le droit: %+v", result)
	}
}

// TCP, TCP avec poignée de main TLS et HTTPS passent tous par la même garde.
func TestCheck_LANRefusedOnTCPAndTLS(t *testing.T) {
	address := listenLoopback(t)
	closed, open := NewChecker(nil, false), NewChecker(nil, true)

	if result := closed.Check(t.Context(), tcpTask(address), checkNow); result.Outcome != OutcomeDown || result.Reason != ReasonLAN {
		t.Fatalf("TCP sans le droit: %+v", result)
	}
	if result := open.Check(t.Context(), tcpTask(address), checkNow); result.Outcome != OutcomeUp {
		t.Fatalf("TCP avec le droit: %+v", result)
	}

	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer secure.Close()
	secureAddress := strings.TrimPrefix(secure.URL, "https://")
	handshake := Task{ID: "p1", Kind: KindTCP, Target: secureAddress, TLS: true, TimeoutSeconds: 2}
	if result := closed.Check(t.Context(), handshake, checkNow); result.Outcome != OutcomeDown || result.Reason != ReasonLAN || result.Certificate != nil {
		t.Fatalf("TLS sans le droit: %+v", result)
	}
	if result := open.Check(t.Context(), handshake, checkNow); result.Reason == ReasonLAN || result.Certificate == nil {
		t.Fatalf("TLS avec le droit: %+v", result)
	}
	// Une chaîne refusée ne doit pas faire passer la sonde par la poignée de
	// main nue quand le réseau local est fermé : pas de certificat lu.
	if result := closed.Check(t.Context(), httpTask(secure.URL), checkNow); result.Outcome != OutcomeDown || result.Reason != ReasonLAN || result.Certificate != nil {
		t.Fatalf("HTTPS sans le droit: %+v", result)
	}
}

// Une redirection ne rouvre pas la porte : le serveur redirige vers lui-même,
// la sonde qui suit les redirections reste refusée dès le premier saut, et
// avec le droit elle les suit.
func TestCheck_LANRefusedWhenFollowingRedirects(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/next" {
			http.Redirect(w, r, server.URL+"/next", http.StatusFound)
		}
	}))
	defer server.Close()
	task := httpTask(server.URL)
	task.FollowRedirects = true

	if result := NewChecker(nil, false).Check(t.Context(), task, checkNow); result.Outcome != OutcomeDown || result.Reason != ReasonLAN {
		t.Fatalf("sans le droit: %+v", result)
	}
	if result := NewChecker(nil, true).Check(t.Context(), task, checkNow); result.Outcome != OutcomeUp {
		t.Fatalf("avec le droit: %+v", result)
	}
}

// Le lien-local reste « adresse refusée » même avec le droit : probe.lan
// n'ouvre pas les métadonnées d'hébergeur.
func TestCheck_LinkLocalStaysRefusedWithTheRight(t *testing.T) {
	for _, allowPrivate := range []bool{false, true} {
		result := NewChecker(nil, allowPrivate).Check(t.Context(), tcpTask("169.254.169.254:80"), checkNow)
		if result.Outcome != OutcomeDown || result.Reason != ReasonAddress {
			t.Errorf("allowPrivate=%v: %+v", allowPrivate, result)
		}
	}
}

// Ce que l'agent écarte revient comme un essai « refusée par l'agent », une
// fois par assignation ; un identifiant vide ou trop long n'en produit pas.
func TestRunner_AssignReportsWhatItRefuses(t *testing.T) {
	sink := newSink()
	runner := NewRunner(sink, NewChecker(nil, true), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	tasks := []Task{
		{ID: "weird", Kind: "ftp", Target: "ftp://a.fr/"},
		{ID: "", Kind: "ftp", Target: "ftp://a.fr/"},
		{ID: strings.Repeat("x", 65), Kind: "ftp", Target: "ftp://a.fr/"},
	}
	runner.Assign(Assignment{Probes: tasks})
	if len(sink.results) != 1 {
		t.Fatalf("attendu un seul essai, obtenu %+v", sink.results)
	}
	got := sink.results[0]
	if got.ProbeID != "weird" || got.Outcome != OutcomeDown || got.Reason != ReasonAgentRefused || got.Code != nil || got.Replayed {
		t.Fatalf("essai: %+v", got)
	}
	if time.Since(got.CheckedAt) > time.Minute {
		t.Fatalf("daté de l'assignation: %s", got.CheckedAt)
	}
	// Une validation du serveur sur ce rapport : il l'accepte tel quel.
	if err := validate(Report{Results: sink.results}, time.Now()); err != nil {
		t.Fatalf("le serveur refuserait le rapport: %v", err)
	}

	runner.Assign(Assignment{Probes: tasks})
	if len(sink.results) != 2 {
		t.Fatalf("un essai par assignation, obtenu %d", len(sink.results))
	}
}

// Un doublon de l'identifiant d'une sonde gardée ne met pas hors ligne la
// sonde qui tourne, et un serveur qui pousse des milliers de tâches fausses
// ne fait que MaxProbesPerMachine essais.
func TestRunner_AssignRefusalsAreBounded(t *testing.T) {
	sink := newSink()
	runner := NewRunner(sink, NewChecker(nil, true), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	good := validTask("good")
	bad := validTask("good")
	bad.Method = "DELETE"
	runner.Assign(Assignment{Probes: []Task{good, bad}})
	if len(sink.results) != 0 {
		t.Fatalf("la sonde gardée n'a rien à se faire reprocher: %+v", sink.results)
	}

	var flood []Task
	for i := range 10000 {
		flood = append(flood, Task{ID: fmt.Sprintf("f%d", i), Kind: "ftp"})
	}
	runner.Assign(Assignment{Probes: flood})
	if len(sink.results) != MaxProbesPerMachine {
		t.Fatalf("attendu %d essais, obtenu %d", MaxProbesPerMachine, len(sink.results))
	}
	seen := map[string]bool{}
	for _, result := range sink.results {
		if seen[result.ProbeID] {
			t.Fatalf("%s deux fois", result.ProbeID)
		}
		seen[result.ProbeID] = true
	}
}
