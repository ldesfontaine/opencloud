package probe

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Le serveur envoie ce que Probe.Task() lui fait bâtir d'une définition
// complétée : l'agent doit l'accepter telle quelle.
func TestTaskValidate_AcceptsWhatTheServerSends(t *testing.T) {
	definitions := map[string]Definition{
		"http par défaut": validDefinition(),
		"http réglé": {
			Name: "x", Kind: KindHTTP, Target: "https://a.fr/health?probe=1", MachineID: "m",
			Interval: 5 * time.Minute, Timeout: 30 * time.Second, Method: "head", ExpectedStatus: "200,301", ExpectedBody: "ok",
		},
		"tcp": {Name: "x", Kind: KindTCP, Target: "db.local:5432", MachineID: "m"},
		"tcp chiffré": {
			Name: "x", Kind: KindTCP, Target: "mail.local:993", MachineID: "m", TLS: true,
			Interval: MinInterval, Timeout: MinTimeout,
		},
		"adresse privée": {Name: "x", Kind: KindHTTP, Target: "http://10.8.0.2:8080/", MachineID: "m"},
	}
	for name, definition := range definitions {
		t.Run(name, func(t *testing.T) {
			completed := definition.Complete()
			if err := completed.Validate(); err != nil {
				t.Fatalf("le serveur lui-même refuse: %v", err)
			}
			task := Probe{ID: "p1", Kind: completed.Kind, Target: completed.Target, Interval: completed.Interval,
				Timeout: completed.Timeout, Method: completed.Method, ExpectedStatus: completed.ExpectedStatus,
				ExpectedBody: completed.ExpectedBody, FollowRedirects: completed.FollowRedirects, TLS: completed.TLS}.Task()
			if err := task.Validate(); err != nil {
				t.Fatalf("tâche du serveur refusée par l'agent: %v (%+v)", err, task)
			}
		})
	}
}

// Les zéros valent les défauts, comme interval() et timeout() : une tâche
// que le serveur n'a pas complétée reste admise.
func TestTaskValidate_AcceptsZeroValuesAsDefaults(t *testing.T) {
	for _, task := range []Task{
		{ID: "p1", Kind: KindHTTP, Target: "https://a.fr/"},
		{ID: "p2", Kind: KindTCP, Target: "a.fr:22"},
		{ID: "p3", Kind: KindHTTP, Target: "https://a.fr/", IntervalSeconds: 60},
	} {
		if err := task.Validate(); err != nil {
			t.Errorf("%+v: %v", task, err)
		}
	}
}

// Ce que le serveur refuse à la création, l'agent le refuse à la réception :
// la commande `probes` n'est pas signée.
func TestTaskValidate_RefusesWhatTheServerRefuses(t *testing.T) {
	cases := []struct {
		name  string
		build func(Task) Task
		want  error
	}{
		{"genre icmp", func(task Task) Task { task.Kind = "icmp"; return task }, ErrKindInvalid},
		{"genre vide", func(task Task) Task { task.Kind = ""; return task }, ErrKindInvalid},
		{"cible vide", func(task Task) Task { task.Target = ""; return task }, ErrTargetInvalid},
		{"cible de 513 caractères", func(task Task) Task { task.Target = "https://a.fr/" + strings.Repeat("a", 500); return task }, ErrTargetInvalid},
		{"cible avec une espace", func(task Task) Task { task.Target = "https://a.fr/ x"; return task }, ErrTargetInvalid},
		{"cible avec un saut de ligne", func(task Task) Task { task.Target = "https://a.fr/\nHost: b"; return task }, ErrTargetInvalid},
		{"cible sans protocole", func(task Task) Task { task.Target = "a.fr"; return task }, ErrTargetInvalid},
		{"cible file", func(task Task) Task { task.Target = "file:///etc/passwd"; return task }, ErrTargetInvalid},
		{"identifiants dans l'URL", func(task Task) Task { task.Target = "https://user:pw@example.org"; return task }, ErrTargetCredentials},
		{"métadonnées du cloud", func(task Task) Task { task.Target = "http://169.254.169.254/latest/meta-data"; return task }, ErrTargetForbidden},
		{"métadonnées AWS en IPv6", func(task Task) Task { task.Target = "http://[fd00:ec2::254]/"; return task }, ErrTargetForbidden},
		{"tcp vers le lien-local", func(task Task) Task { task.Kind = KindTCP; task.Target = "[fe80::1]:22"; return task }, ErrTargetForbidden},
		{"tcp sans port", func(task Task) Task { task.Kind = KindTCP; task.Target = "a.fr"; return task }, ErrTargetInvalid},
		{"intervalle d'une seconde", func(task Task) Task { task.IntervalSeconds = 1; return task }, ErrIntervalInvalid},
		{"intervalle de 25 heures", func(task Task) Task { task.IntervalSeconds = 25 * 3600; return task }, ErrIntervalInvalid},
		{"intervalle négatif", func(task Task) Task { task.IntervalSeconds = -60; return task }, ErrIntervalInvalid},
		{"intervalle qui déborde une durée", func(task Task) Task { task.IntervalSeconds = 1 << 40; return task }, ErrIntervalInvalid},
		{"délai de 31 secondes", func(task Task) Task { task.TimeoutSeconds = 31; return task }, ErrTimeoutInvalid},
		{"délai négatif", func(task Task) Task { task.TimeoutSeconds = -1; return task }, ErrTimeoutInvalid},
		{"délai qui déborde une durée", func(task Task) Task { task.TimeoutSeconds = 1 << 40; return task }, ErrTimeoutInvalid},
		{"méthode DELETE", func(task Task) Task { task.Method = "DELETE"; return task }, ErrMethodInvalid},
		{"méthode CONNECT", func(task Task) Task { task.Method = "CONNECT"; return task }, ErrMethodInvalid},
		{"méthode en minuscules", func(task Task) Task { task.Method = "get"; return task }, ErrMethodInvalid},
		{"motif de code illisible", func(task Task) Task { task.ExpectedStatus = "abc"; return task }, ErrStatusInvalid},
		{"famille de codes hors de 1 à 5", func(task Task) Task { task.ExpectedStatus = "9xx"; return task }, ErrStatusInvalid},
		{"texte attendu de 201 caractères", func(task Task) Task { task.ExpectedBody = strings.Repeat("é", 201); return task }, ErrBodyInvalid},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			task := testCase.build(Task{ID: "p1", Kind: KindHTTP, Target: "https://a.fr/", IntervalSeconds: 60, TimeoutSeconds: 5, Method: "GET", ExpectedStatus: "2xx"})
			err := task.Validate()
			if !errors.Is(err, testCase.want) {
				t.Fatalf("refus attendu %v, obtenu %v", testCase.want, err)
			}
		})
	}
}

// Avec les bornes d'aujourd'hui (délai ≤ 30 s, intervalle ≥ 30 s), un délai
// n'excède jamais l'intervalle : la comparaison sur les valeurs effectives
// garde la règle du serveur si l'une des bornes bouge.
func TestTaskValidate_TimeoutMayEqualTheInterval(t *testing.T) {
	task := Task{ID: "p1", Kind: KindTCP, Target: "a.fr:22", IntervalSeconds: 30, TimeoutSeconds: 30}
	if err := task.Validate(); err != nil {
		t.Fatalf("délai égal à l'intervalle: %v", err)
	}
	task.TimeoutSeconds = 0
	if err := task.Validate(); err != nil {
		t.Fatalf("délai par défaut: %v", err)
	}
}

// Une sonde TCP n'a ni méthode ni code attendu : leurs champs, vides ou non,
// ne la font pas refuser (Complete les efface côté serveur).
func TestTaskValidate_TCPIgnoresHTTPFields(t *testing.T) {
	task := Task{ID: "p1", Kind: KindTCP, Target: "a.fr:22", Method: "DELETE", ExpectedStatus: "abc"}
	if err := task.Validate(); err != nil {
		t.Fatalf("refusée à tort: %v", err)
	}
}

func validTask(id string) Task {
	return Task{ID: id, Kind: KindHTTP, Target: "https://a.fr/" + id, IntervalSeconds: 60, TimeoutSeconds: 5, Method: "GET", ExpectedStatus: "2xx"}
}

func TestSanitize_KeepsOrderDropsDuplicatesAndCaps(t *testing.T) {
	var tasks []Task
	for i := range MaxProbesPerMachine + 6 {
		tasks = append(tasks, validTask(fmt.Sprintf("p%02d", i)))
	}
	kept, dropped := Sanitize(tasks)
	if len(kept) != MaxProbesPerMachine || len(dropped) != 6 {
		t.Fatalf("gardées %d, écartées %d", len(kept), len(dropped))
	}
	for i, task := range kept {
		if task.ID != fmt.Sprintf("p%02d", i) {
			t.Fatalf("ordre perdu à %d: %s", i, task.ID)
		}
	}
	for _, task := range dropped {
		if task.Reason != DroppedOverLimit {
			t.Fatalf("raison %q", task.Reason)
		}
	}
}

func TestSanitize_DropsInvalidEmptyAndRepeatedIDs(t *testing.T) {
	forbidden := validTask("meta")
	forbidden.Target = "http://169.254.169.254/latest/meta-data"
	repeated := validTask("a")
	repeated.Target = "https://elsewhere.example/"
	tasks := []Task{
		validTask("a"),
		forbidden,
		repeated,
		validTask(""),
		validTask(strings.Repeat("x", 65)),
		validTask("b"),
	}
	kept, dropped := Sanitize(tasks)
	if len(kept) != 2 || kept[0].ID != "a" || kept[0].Target != "https://a.fr/a" || kept[1].ID != "b" {
		t.Fatalf("gardées: %+v", kept)
	}
	reasons := make([]string, 0, len(dropped))
	for _, task := range dropped {
		reasons = append(reasons, task.Reason)
	}
	want := []string{"probe.target_forbidden", DroppedIDRepeated, DroppedIDInvalid, DroppedIDInvalid}
	if fmt.Sprint(reasons) != fmt.Sprint(want) {
		t.Fatalf("raisons %v, attendu %v", reasons, want)
	}
}

// Une tâche invalide n'occupe pas une place des 64 : elle ne chasse pas une
// sonde valable.
func TestSanitize_InvalidTasksDoNotTakeASlot(t *testing.T) {
	var tasks []Task
	for i := range 10 {
		bad := validTask(fmt.Sprintf("bad%d", i))
		bad.Method = "DELETE"
		tasks = append(tasks, bad)
	}
	for i := range MaxProbesPerMachine {
		tasks = append(tasks, validTask(fmt.Sprintf("p%02d", i)))
	}
	kept, dropped := Sanitize(tasks)
	if len(kept) != MaxProbesPerMachine || len(dropped) != 10 {
		t.Fatalf("gardées %d, écartées %d", len(kept), len(dropped))
	}
}

// Le journal garde l'hôte, jamais le chemin, la requête ni les identifiants
// d'une cible : une URL peut porter un jeton.
func TestSanitize_DroppedHostNeverCarriesThePathOrTheQuery(t *testing.T) {
	task := validTask("p1")
	task.Target = "https://user:hunter2@internal.example:8443/admin/reset?token=s3cr3t#frag"
	task.Method = "DELETE"
	_, dropped := Sanitize([]Task{task})
	if len(dropped) != 1 {
		t.Fatalf("écartées: %+v", dropped)
	}
	got := dropped[0]
	if got.Host != "internal.example" || got.ID != "p1" || got.Kind != KindHTTP {
		t.Fatalf("écartée: %+v", got)
	}
	// Une cible illisible n'a pas d'hôte, et ne fuit pas non plus.
	bad := validTask("p2")
	bad.Target = "token=s3cr3t not a url"
	_, dropped = Sanitize([]Task{bad})
	if dropped[0].Host != "" {
		t.Fatalf("hôte d'une cible illisible: %q", dropped[0].Host)
	}
}

// Un serveur qui forge des champs géants ne remplit pas le journal.
func TestSanitize_DroppedFieldsAreBounded(t *testing.T) {
	task := validTask("p1")
	task.ID = strings.Repeat("i", 5000)
	task.Kind = Kind(strings.Repeat("k", 5000))
	task.Target = "https://" + strings.Repeat("h", 5000) + "/"
	_, dropped := Sanitize([]Task{task})
	got := dropped[0]
	if len(got.ID) > 64 || len(got.Kind) > 16 || len(got.Host) > 255 {
		t.Fatalf("champs non bornés: %d %d %d", len(got.ID), len(got.Kind), len(got.Host))
	}
}

// Assign n'applique que les tâches gardées, et dit celles qu'il écarte.
func TestRunner_AssignNeverStartsARefusedTask(t *testing.T) {
	sink := newSink()
	runner, _ := testRunner(t, sink)
	refused := []Task{
		{ID: "meta", Kind: KindHTTP, Target: "http://169.254.169.254/latest/meta-data", IntervalSeconds: 60},
		{ID: "delete", Kind: KindHTTP, Target: "https://a.fr/", IntervalSeconds: 60, Method: "DELETE"},
		{ID: "fast", Kind: KindTCP, Target: "a.fr:22", IntervalSeconds: 1},
		{ID: "icmp", Kind: "icmp", Target: "a.fr", IntervalSeconds: 60},
	}
	if got := runner.Assign(Assignment{Probes: refused}); got != len(refused) {
		t.Fatalf("écartées %d, attendu %d", got, len(refused))
	}
	time.Sleep(50 * time.Millisecond)
	if runner.Count() != 0 {
		t.Fatalf("%d sondes lancées pour un jeu entièrement refusé", runner.Count())
	}
	// Un jeu mêlé : seule la bonne tâche part.
	good := Task{ID: "good", Kind: KindTCP, Target: "127.0.0.1:1", IntervalSeconds: 60, TimeoutSeconds: 1}
	if got := runner.Assign(Assignment{Probes: append(refused, good)}); got != len(refused) {
		t.Fatalf("écartées %d, attendu %d", got, len(refused))
	}
	// Run démarre à part : un jeu reçu avant lui s'applique à son départ.
	deadline := time.Now().Add(3 * time.Second)
	for runner.Count() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if runner.Count() != 1 {
		t.Fatalf("%d sondes en cours, attendu 1", runner.Count())
	}
}

// Un jeu refusé reçu avant le démarrage attend lui aussi le nettoyage.
func TestRunner_AssignBeforeRunKeepsOnlyTheKeptTasks(t *testing.T) {
	sink := newSink()
	runner := NewRunner(sink, NewChecker(nil, true), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	runner.Assign(Assignment{Probes: []Task{
		{ID: "meta", Kind: KindHTTP, Target: "http://169.254.169.254/", IntervalSeconds: 60},
		{ID: "good", Kind: KindTCP, Target: "127.0.0.1:1", IntervalSeconds: 60, TimeoutSeconds: 1},
	}})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.wanted) != 1 || runner.wanted[0].ID != "good" {
		t.Fatalf("attendues: %+v", runner.wanted)
	}
}

// L'agent dit ce qu'il écarte, sans la cible entière, et borne son journal.
func TestRunner_LogsWhatItRefusesWithoutTheTarget(t *testing.T) {
	var logs lockedBuffer
	runner := NewRunner(newSink(), NewChecker(nil, true), slog.New(slog.NewTextHandler(&logs, nil)))
	var tasks []Task
	for i := range 12 {
		task := validTask(fmt.Sprintf("p%02d", i))
		task.Target = "https://internal.example/reset?token=s3cr3t"
		task.Method = "DELETE"
		tasks = append(tasks, task)
	}
	if got := runner.Assign(Assignment{Probes: tasks}); got != 12 {
		t.Fatalf("écartées %d", got)
	}
	text := logs.String()
	if strings.Count(text, "probe refused by the agent") != 8 {
		t.Fatalf("une ligne par tâche, 8 au plus:\n%s", text)
	}
	if !strings.Contains(text, "more=4") || !strings.Contains(text, "host=internal.example") || !strings.Contains(text, "probe.method_invalid") {
		t.Fatalf("le journal manque d'un champ:\n%s", text)
	}
	if strings.Contains(text, "s3cr3t") || strings.Contains(text, "reset") {
		t.Fatalf("le journal porte la cible:\n%s", text)
	}
}
