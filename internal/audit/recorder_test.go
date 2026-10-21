package audit_test

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

var testNow = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

func newRecorder(t *testing.T) (*audit.Recorder, *store.DB, *bytes.Buffer) {
	t.Helper()
	db := storetest.Open(t)
	var logs bytes.Buffer
	recorder := audit.New(db, slog.New(slog.NewTextHandler(&logs, nil)))
	recorder.SetClock(func() time.Time { return testNow })
	return recorder, db, &logs
}

func listAll(t *testing.T, db *store.DB) []audit.Event {
	t.Helper()
	events, err := db.ListAuditEvents(context.Background(), audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// L'acteur et le passage d'apply viennent du contexte ; la sorte d'objet,
// de l'action.
func TestRecord_TakesActorAndApplyFromTheContext(t *testing.T) {
	recorder, db, _ := newRecorder(t)
	ctx := audit.WithActor(context.Background(), audit.Operator("lucas"))
	recorder.Record(ctx, audit.Event{Action: audit.ProbeCreate, ObjectID: "p1", ObjectName: "api", Details: audit.Details{"kind": "http"}})
	events := listAll(t, db)
	if len(events) != 1 {
		t.Fatalf("events: %+v", events)
	}
	got := events[0]
	if got.Actor != audit.Operator("lucas") || got.ObjectKind != audit.ObjectProbe || got.ObjectName != "api" ||
		!got.At.Equal(testNow) || got.ApplyID != "" || got.Details["kind"] != "http" {
		t.Fatalf("event: %+v", got)
	}
}

// Sans acteur, l'événement est écrit quand même, sous « unknown », et le
// journal du processus le dit : un oubli de câblage se voit.
func TestRecord_WithoutActor_WritesUnknownAndWarns(t *testing.T) {
	recorder, db, logs := newRecorder(t)
	recorder.Record(context.Background(), audit.Event{Action: audit.ProjectCreate, ObjectID: "prj_1"})
	events := listAll(t, db)
	if len(events) != 1 || events[0].Actor.Kind != audit.ActorUnknown {
		t.Fatalf("events: %+v", events)
	}
	if !strings.Contains(logs.String(), "audit event without actor") {
		t.Fatalf("no warning: %s", logs.String())
	}
}

// Un détail dont le nom dit un secret ne s'écrit jamais, et c'est dit en
// erreur ; un détail vide ne s'écrit pas non plus.
func TestRecord_DropsSecretAndEmptyDetails(t *testing.T) {
	recorder, db, logs := newRecorder(t)
	ctx := audit.WithActor(context.Background(), audit.CLI("root"))
	recorder.Record(ctx, audit.Event{Action: audit.ChannelUpdate, ObjectID: "1", Details: audit.Details{
		"password": "x", "token": "x", "secret": "x", "url": "x", "webhook_url": "x", "client_secret": "x",
		"secret_changed": "true", "token_id": "t1", "format": "discord", "destination": "",
	}})
	got := listAll(t, db)[0].Details
	want := audit.Details{"secret_changed": "true", "token_id": "t1", "format": "discord"}
	if len(got) != len(want) {
		t.Fatalf("details %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("details %v, want %v", got, want)
		}
	}
	if !strings.Contains(logs.String(), "audit detail dropped") {
		t.Fatalf("no error: %s", logs.String())
	}
}

func TestRecord_NilRecorderDoesNothing(t *testing.T) {
	var recorder *audit.Recorder
	recorder.Record(context.Background(), audit.Event{Action: audit.ProbeCreate})
	if err := recorder.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// La purge retire ce qui a plus d'un an, et rien d'autre.
func TestPurge_RemovesOnlyWhatIsOlderThanTheRetention(t *testing.T) {
	recorder, db, _ := newRecorder(t)
	ctx := audit.WithActor(context.Background(), audit.Rule(""))
	recorder.SetClock(func() time.Time { return testNow.Add(-audit.Retention - time.Hour) })
	recorder.Record(ctx, audit.Event{Action: audit.ProbeDelete, ObjectID: "old"})
	recorder.SetClock(func() time.Time { return testNow.Add(-audit.Retention + time.Hour) })
	recorder.Record(ctx, audit.Event{Action: audit.ProbeDelete, ObjectID: "kept"})
	recorder.SetClock(func() time.Time { return testNow })
	if err := recorder.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	events := listAll(t, db)
	if len(events) != 1 || events[0].ObjectID != "kept" {
		t.Fatalf("events: %+v", events)
	}
}

// Chaque action vise une sorte d'objet de la liste, chaque sorte a au moins
// une action, et un verbe se lit sans l'objet.
func TestActions_NameAKnownObjectKindAndAVerb(t *testing.T) {
	used := map[audit.ObjectKind]bool{}
	for _, action := range audit.Actions() {
		kind := action.ObjectKind()
		if !slices.Contains(audit.ObjectKinds(), kind) {
			t.Errorf("%s: unknown object kind %q", action, kind)
		}
		if action.Verb() == "" || strings.Contains(action.Verb(), ".") {
			t.Errorf("%s: verb %q", action, action.Verb())
		}
		used[kind] = true
	}
	for _, kind := range audit.ObjectKinds() {
		if !used[kind] {
			t.Errorf("object kind %q has no action", kind)
		}
	}
	seen := map[string]bool{}
	for _, verb := range audit.Verbs() {
		if seen[verb] {
			t.Errorf("verb %q listed twice", verb)
		}
		seen[verb] = true
	}
}

func TestActorFrom_EmptyContextIsUnknown(t *testing.T) {
	if got := audit.ActorFrom(context.Background()); got.Kind != audit.ActorUnknown {
		t.Fatalf("actor: %+v", got)
	}
	if got := audit.ActorFrom(audit.WithActor(context.Background(), audit.Agent("m1"))); got != audit.Agent("m1") {
		t.Fatalf("actor: %+v", got)
	}
}

// forwardedEvents garde ce que le Recorder confie à l'envoi.
type forwardedEvents struct {
	events []audit.Event
}

func (f *forwardedEvents) ForwardAuditEvent(_ context.Context, event audit.Event) {
	f.events = append(f.events, event)
}

// L'envoi reçoit l'événement tel qu'il est écrit : son identifiant en base,
// et des détails déjà nettoyés (aucun secret ne sort par là).
func TestRecord_ForwardsTheWrittenEventWithItsID(t *testing.T) {
	recorder, db, _ := newRecorder(t)
	forwarded := &forwardedEvents{}
	recorder.SetForwarder(forwarded)
	ctx := audit.WithActor(context.Background(), audit.Operator("lucas"))
	recorder.Record(ctx, audit.Event{Action: audit.ChannelUpdate, ObjectID: "1", ObjectName: "chat", Details: audit.Details{
		"webhook_url": "https://hooks.example.org/x", "format": "discord", "destination": "",
	}})
	if len(forwarded.events) != 1 {
		t.Fatalf("%d événements confiés, 1 attendu", len(forwarded.events))
	}
	got := forwarded.events[0]
	written := listAll(t, db)[0]
	if got.ID == 0 || got.ID != written.ID {
		t.Errorf("identifiant : %d, la base a %d", got.ID, written.ID)
	}
	if len(got.Details) != 1 || got.Details["format"] != "discord" {
		t.Errorf("détails non nettoyés : %v", got.Details)
	}
	if got.Actor != audit.Operator("lucas") || got.ObjectKind != audit.ObjectChannel || !got.At.Equal(testNow) {
		t.Errorf("événement : %+v", got)
	}
}

func TestRecord_WithoutForwarder_StillWrites(t *testing.T) {
	recorder, db, _ := newRecorder(t)
	ctx := audit.WithActor(context.Background(), audit.Operator("lucas"))
	recorder.Record(ctx, audit.Event{Action: audit.ProbePause, ObjectID: "p1"})
	if events := listAll(t, db); len(events) != 1 {
		t.Fatalf("events: %+v", events)
	}
}
