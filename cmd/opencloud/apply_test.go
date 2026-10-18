package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const planWithChanges = `{
  "language": "fr", "file_hash": "sha256:1a2b3c4d5e6f7a8b9c0d", "prune": false, "plan_hash": "sha256:plan1", "applicable": true,
  "summary": {"create": 1, "update": 2, "delete": 1, "unchanged": 14, "unmanaged": 3},
  "changes": [
    {"action": "create", "kind": "project", "id": "", "name": "docs", "fields": [{"field": "name", "before": "", "after": "docs"}], "notes": [], "carries": []},
    {"action": "update", "kind": "service", "id": "svc_7f3k9a0b1c2d3e4f", "name": "app-1/wiki",
     "fields": [{"field": "units[app].version", "before": "2.4.0", "after": "2.4.1"}, {"field": "public_url", "before": "", "after": "https://wiki.example.org"}], "notes": [], "carries": []},
    {"action": "update", "kind": "probe", "id": "00000000000000c1", "name": "wiki-http",
     "fields": [{"field": "interval", "before": "1m", "after": "30s"}], "notes": [], "carries": []},
    {"action": "delete", "kind": "probe", "id": "a1b2c3d4e5f60718", "name": "old-check", "fields": [], "notes": [],
     "carries": [{"what": "probe_results", "count": 1204}, {"what": "probe_days", "count": 80}]}
  ],
  "unmanaged": [], "problems": []
}`

const emptyPlan = `{"language": "fr", "file_hash": "sha256:1a2b3c4d5e6f7a8b9c0d", "prune": false, "plan_hash": "sha256:empty", "applicable": true,
  "summary": {"create": 0, "update": 0, "delete": 0, "unchanged": 14, "unmanaged": 0}, "changes": [], "unmanaged": [], "problems": []}`

const appliedAnswer = `{"id": "apl_0123456789abcdef", "state": "applied", "done": 4, "error": "", "error_object": "",
  "summary": {"create": 1, "update": 2, "delete": 1, "unchanged": 14}}`

// fakeServer joue le serveur : les plans qu'il rend à la file, les réponses
// d'apply, et ce qu'il a reçu.
type fakeServer struct {
	mu        sync.Mutex
	plans     []string
	applies   []fakeAnswer
	planCalls int
	received  []map[string]any
}

type fakeAnswer struct {
	status int
	body   string
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/manifest/plan", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.planCalls++
		plan := f.plans[0]
		if len(f.plans) > 1 {
			f.plans = f.plans[1:]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, plan)
	})
	mux.HandleFunc("POST /api/manifest/apply", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		f.received = append(f.received, request)
		answer := f.applies[0]
		if len(f.applies) > 1 {
			f.applies = f.applies[1:]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(answer.status)
		_, _ = io.WriteString(w, answer.body)
	})
	return mux
}

func (f *fakeServer) applyCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.received)
}

func writeManifestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(shortDir(t), "opencloud.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runApplyCommand lance la commande contre un faux serveur sur la socket.
func runApplyCommand(t *testing.T, fake *fakeServer, input string, interactive bool, flags ...string) (string, error) {
	t.Helper()
	configPath, stateDir := writeShortConfig(t)
	serveOnControlSocket(t, stateDir, fake.handler())
	file := writeManifestFile(t, "version: 1\nmachines:\n  - name: opencloud\n")
	var out bytes.Buffer
	args := append([]string{"-config", configPath}, flags...)
	err := applyCommand(append(args, file), strings.NewReader(input), &out, interactive)
	return out.String(), err
}

func exitCode(err error) int {
	var refused *refusal
	switch {
	case err == nil:
		return 0
	case errors.As(err, &refused):
		return 2
	}
	return 1
}

func TestApplyCommand_ShowsThePlanAndAsks(t *testing.T) {
	fake := &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusCreated, appliedAnswer}}}
	out, err := runApplyCommand(t, fake, "o\n", true, "-prune")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"Plan pour opencloud.yaml (sha256:1a2b3c4d…)\n",
		"  + projet docs\n",
		"  ~ service app-1/wiki (svc_7f3k…)\n      units[app].version : 2.4.0 → 2.4.1\n      public_url : ∅ → https://wiki.example.org\n",
		"  ~ sonde wiki-http : interval 1m → 30s\n",
		"  - sonde old-check (a1b2…) — emporte 1 204 essais, 80 jours\n",
		"  = 14 inchangés · 3 hors fichier, gardés (-prune pour les supprimer)\n",
		"Appliquer ces changements ? [o/N] ",
		"Appliqué : 4 changements écrits (apl_0123456789abcdef).\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(fake.received) != 1 {
		t.Fatalf("apply calls: %d", len(fake.received))
	}
	got := fake.received[0]
	if got["plan_hash"] != "sha256:plan1" || got["prune"] != true || got["file_name"] != "opencloud.yaml" || got["actor_name"] == nil || got["actor_name"] == "" {
		t.Fatalf("request: %+v", got)
	}
	if !strings.Contains(got["file"].(string), "version: 1") {
		t.Fatalf("the file must travel as is: %+v", got)
	}
}

func TestApplyCommand_AnsweringNoWritesNothing(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "non\n", ""} {
		fake := &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusCreated, appliedAnswer}}}
		out, err := runApplyCommand(t, fake, answer, true)
		if exitCode(err) != 2 || fake.applyCalls() != 0 {
			t.Errorf("answer %q: exit %d, calls %d, %v\n%s", answer, exitCode(err), fake.applyCalls(), err, out)
		}
	}
}

func TestApplyCommand_NoTerminalWithoutYesRefuses(t *testing.T) {
	fake := &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusCreated, appliedAnswer}}}
	out, err := runApplyCommand(t, fake, "o\n", false)
	if exitCode(err) != 2 || fake.applyCalls() != 0 {
		t.Fatalf("exit %d, calls %d, %v", exitCode(err), fake.applyCalls(), err)
	}
	if !strings.Contains(err.Error(), "-yes") || strings.Contains(out, "[o/N]") {
		t.Fatalf("it must say how to proceed, and not ask a question nobody can answer:\n%v\n%s", err, out)
	}

	fake = &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusCreated, appliedAnswer}}}
	out, err = runApplyCommand(t, fake, "", false, "-yes")
	if err != nil || fake.applyCalls() != 1 || strings.Contains(out, "[o/N]") {
		t.Fatalf("with -yes: %v, calls %d\n%s", err, fake.applyCalls(), out)
	}
}

func TestApplyCommand_DryRunWritesNothing(t *testing.T) {
	fake := &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusCreated, appliedAnswer}}}
	out, err := runApplyCommand(t, fake, "o\n", true, "-dry-run", "-yes")
	if err != nil || fake.applyCalls() != 0 {
		t.Fatalf("%v, calls %d", err, fake.applyCalls())
	}
	if !strings.Contains(out, "+ projet docs") || !strings.Contains(out, "Simulation") || strings.Contains(out, "[o/N]") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestApplyCommand_NothingToChangeCallsNoApply(t *testing.T) {
	fake := &fakeServer{plans: []string{emptyPlan}, applies: []fakeAnswer{{http.StatusCreated, appliedAnswer}}}
	out, err := runApplyCommand(t, fake, "", false)
	if err != nil || fake.applyCalls() != 0 || !strings.Contains(out, "Rien à changer.\n") {
		t.Fatalf("%v, calls %d\n%s", err, fake.applyCalls(), out)
	}
}

func TestApplyCommand_ReasksOnceWhenThePlanChanged(t *testing.T) {
	changed := strings.Replace(planWithChanges, "sha256:plan1", "sha256:plan2", 1)
	stale := `{"error": "manifest.plan_changed", "plan": ` + changed + `}`
	fake := &fakeServer{
		plans:   []string{planWithChanges},
		applies: []fakeAnswer{{http.StatusConflict, stale}, {http.StatusCreated, appliedAnswer}},
	}
	out, err := runApplyCommand(t, fake, "o\no\n", true)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Count(out, "Appliquer ces changements ? [o/N] ") != 2 || strings.Count(out, "Plan pour") != 2 {
		t.Fatalf("the new plan must be shown and confirmed again:\n%s", out)
	}
	if !strings.Contains(out, "L'annuaire a changé depuis le plan") {
		t.Fatalf("it must say why:\n%s", out)
	}
	if len(fake.received) != 2 || fake.received[0]["plan_hash"] != "sha256:plan1" || fake.received[1]["plan_hash"] != "sha256:plan2" {
		t.Fatalf("requests: %+v", fake.received)
	}

	// Une deuxième fois : l'annuaire bouge trop, la commande s'arrête.
	fake = &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusConflict, stale}}}
	_, err = runApplyCommand(t, fake, "o\no\no\n", true)
	if exitCode(err) != 1 || fake.applyCalls() != 2 {
		t.Fatalf("exit %d, calls %d, %v", exitCode(err), fake.applyCalls(), err)
	}

	// Refuser le nouveau plan ne l'applique pas.
	fake = &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusConflict, stale}, {http.StatusCreated, appliedAnswer}}}
	_, err = runApplyCommand(t, fake, "o\nn\n", true)
	if exitCode(err) != 2 || fake.applyCalls() != 1 {
		t.Fatalf("exit %d, calls %d, %v", exitCode(err), fake.applyCalls(), err)
	}
}

func TestApplyCommand_RefusedFileShowsEveryLineAndExits2(t *testing.T) {
	refused := `{"language": "fr", "file_hash": "sha256:x", "prune": false, "plan_hash": "sha256:y", "applicable": false,
	  "summary": {"create": 0, "update": 0, "delete": 0, "unchanged": 0, "unmanaged": 0}, "changes": [], "unmanaged": [],
	  "problems": [
	    {"key": "manifest.machine_unknown", "line": 12, "column": 0, "path": "wiki-http", "value": "app-3"},
	    {"key": "probe.threshold_invalid", "line": 20, "column": 0, "path": "mail", "value": ""},
	    {"key": "manifest.swap_refused", "line": 0, "column": 0, "path": "", "value": "app-1/a, app-1/b"}
	  ]}`
	fake := &fakeServer{plans: []string{refused}, applies: []fakeAnswer{{http.StatusCreated, appliedAnswer}}}
	out, err := runApplyCommand(t, fake, "o\n", true, "-yes")
	if exitCode(err) != 2 || fake.applyCalls() != 0 {
		t.Fatalf("exit %d, calls %d, %v", exitCode(err), fake.applyCalls(), err)
	}
	for _, want := range []string{
		"Ligne 12 : machine inconnue « app-3 ».",
		"Ligne 20 (mail) : ",
		"Ces services s'échangent leurs clés (app-1/a, app-1/b)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "[manifest.") || strings.Contains(out, "%!") {
		t.Errorf("a key has no text:\n%s", out)
	}
}

func TestApplyCommand_FailedApplyNamesTheStepAndHowToResume(t *testing.T) {
	failed := `{"id": "apl_00000000000000f1", "state": "failed", "done": 2, "error": "directory.name_taken", "error_object": "probe/wiki-http",
	  "summary": {"create": 1, "update": 2, "delete": 2, "unchanged": 0}}`
	fake := &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusCreated, failed}}}
	_, err := runApplyCommand(t, fake, "", false, "-yes")
	if exitCode(err) != 1 {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
	for _, want := range []string{"Arrêté au changement 3/5 (sonde wiki-http) : ", "Les 2 premiers sont écrits", "relancez"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "[directory.") {
		t.Errorf("the key must be translated: %v", err)
	}
}

func TestApplyCommand_ServerRefusalsAreSaidInTheInterfaceLanguage(t *testing.T) {
	english := strings.Replace(planWithChanges, `"language": "fr"`, `"language": "en"`, 1)
	busy := `{"error": "manifest.busy"}`
	fake := &fakeServer{plans: []string{english}, applies: []fakeAnswer{{http.StatusConflict, busy}}}
	out, err := runApplyCommand(t, fake, "", false, "-yes")
	if exitCode(err) != 1 || !strings.Contains(err.Error(), "Another apply is running.") {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
	if !strings.Contains(out, "+ project docs") {
		t.Fatalf("kinds follow the interface language:\n%s", out)
	}
}

func TestApplyCommand_NotRunningServerSaysSo(t *testing.T) {
	configPath, _ := writeShortConfig(t)
	file := writeManifestFile(t, "version: 1\n")
	err := applyCommand([]string{"-config", configPath, file}, strings.NewReader(""), io.Discard, true)
	if exitCode(err) != 1 || !strings.Contains(err.Error(), "le serveur ne tourne pas") {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
}

func TestApplyCommand_NeedsExactlyOneFile(t *testing.T) {
	configPath, _ := writeShortConfig(t)
	for _, args := range [][]string{{"-config", configPath}, {"-config", configPath, "a.yaml", "b.yaml"}} {
		if err := applyCommand(args, strings.NewReader(""), io.Discard, true); exitCode(err) != 2 {
			t.Errorf("%v: exit %d, %v", args, exitCode(err), err)
		}
	}
	if err := applyCommand([]string{"-config", configPath, filepath.Join(shortDir(t), "absent.yaml")}, strings.NewReader(""), io.Discard, true); exitCode(err) != 1 {
		t.Errorf("a file that does not exist is an error: %v", err)
	}
}

// -yes confirme le plan qu'on a lu au lancement, pas un autre : un plan qui a
// changé entre-temps (avec -prune, une suppression apparue) se montre, rien
// ne s'écrit, et la commande sort en erreur.
func TestApplyCommand_YesNeverAppliesAChangedPlan(t *testing.T) {
	changed := strings.Replace(planWithChanges, "sha256:plan1", "sha256:plan2", 1)
	changed = strings.Replace(changed, `"name": "old-check"`, `"name": "created-in-the-ui-meanwhile"`, 1)
	stale := `{"error": "manifest.plan_changed", "plan": ` + changed + `}`
	fake := &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusConflict, stale}, {http.StatusCreated, appliedAnswer}}}
	out, err := runApplyCommand(t, fake, "", false, "-yes", "-prune")
	if exitCode(err) != 1 || fake.applyCalls() != 1 {
		t.Fatalf("exit %d, calls %d, %v\n%s", exitCode(err), fake.applyCalls(), err, out)
	}
	if !strings.Contains(err.Error(), "l'annuaire a changé pendant la commande : relancez-la") {
		t.Errorf("it must say to run again: %v", err)
	}
	if !strings.Contains(out, "created-in-the-ui-meanwhile") || !strings.Contains(out, "L'annuaire a changé depuis le plan") {
		t.Errorf("the new plan must be shown:\n%s", out)
	}
	// Même en terminal : -yes ne redemande pas, il n'applique pas non plus.
	fake = &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusConflict, stale}, {http.StatusCreated, appliedAnswer}}}
	_, err = runApplyCommand(t, fake, "o\n", true, "-yes")
	if exitCode(err) != 1 || fake.applyCalls() != 1 {
		t.Fatalf("with a terminal: exit %d, calls %d, %v", exitCode(err), fake.applyCalls(), err)
	}
}

func TestApplyCommand_AFlagMistakeExits2(t *testing.T) {
	configPath, _ := writeShortConfig(t)
	err := applyCommand([]string{"-config", configPath, "-nope", "a.yaml"}, strings.NewReader(""), io.Discard, true)
	if exitCode(err) != 2 {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
}

// Un arrêt sans clé de catalogue se lit quand même : jamais la clé brute.
func TestApplyCommand_AFailureWithoutKeyIsStillReadable(t *testing.T) {
	failed := `{"id": "apl_00000000000000f1", "state": "failed", "done": 0, "error": "", "error_object": "probe/wiki-http",
	  "summary": {"create": 1, "update": 2, "delete": 1, "unchanged": 0}}`
	fake := &fakeServer{plans: []string{planWithChanges}, applies: []fakeAnswer{{http.StatusCreated, failed}}}
	_, err := runApplyCommand(t, fake, "", false, "-yes")
	if exitCode(err) != 1 || strings.Contains(err.Error(), "manifest.apply_failed") || !strings.Contains(err.Error(), "Un changement a été refusé.") {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
}

// Les comptes s'accordent : « 1 inchangé », « 1 essai », et les milliers se
// séparent comme la langue du plan le veut.
func TestApplyCommand_CountsAgreeAndThousandsAreSeparated(t *testing.T) {
	single := strings.NewReplacer(
		`"unchanged": 14, "unmanaged": 3`, `"unchanged": 1, "unmanaged": 1`,
		`{"what": "probe_results", "count": 1204}, {"what": "probe_days", "count": 80}`, `{"what": "probe_results", "count": 1}, {"what": "probe_days", "count": 1}`,
	).Replace(planWithChanges)
	fake := &fakeServer{plans: []string{single}, applies: []fakeAnswer{{http.StatusCreated, appliedAnswer}}}
	out, err := runApplyCommand(t, fake, "", false, "-yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"= 1 inchangé · 1 hors fichier, gardé (", "— emporte 1 essai, 1 jour\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	english := strings.Replace(planWithChanges, `"language": "fr"`, `"language": "en"`, 1)
	fake = &fakeServer{plans: []string{english}, applies: []fakeAnswer{{http.StatusCreated, appliedAnswer}}}
	out, err = runApplyCommand(t, fake, "", false, "-yes")
	if err != nil || !strings.Contains(out, "1,204 results") {
		t.Fatalf("%v\n%s", err, out)
	}
}
