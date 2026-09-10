package domain

import (
	"context"
	"log/slog"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// fakeStore retient ce que le recorder écrit : c'est tout ce qu'on éprouve
// ici, l'écriture réelle est celle de store.
type fakeStore struct {
	recorded  []store.Domain
	forgotten []string
	lines     []store.ActionLine
}

func (f *fakeStore) RecordDomain(_ context.Context, domain store.Domain) error {
	f.recorded = append(f.recorded, domain)
	return nil
}

func (f *fakeStore) ForgetDomain(_ context.Context, name string) error {
	f.forgotten = append(f.forgotten, name)
	return nil
}

func (f *fakeStore) Lines(_ context.Context, _ string, _ int64) ([]store.ActionLine, error) {
	return f.lines, nil
}

func newRecorder(lines ...string) (*Recorder, *fakeStore) {
	recorded := &fakeStore{}
	for index, text := range lines {
		recorded.lines = append(recorded.lines, store.ActionLine{Seq: int64(index + 1), Text: text})
	}
	return New(recorded, slog.New(slog.DiscardHandler)), recorded
}

func publication(state store.ActionState, result string) store.Action {
	return store.Action{
		ID:        "vhost-260910-120000-abcd",
		MachineID: "local",
		Kind:      string(catalog.KindVhost),
		Params: catalog.VhostPublication{
			Domain:      "temoin.exemple.fr",
			Environment: "prod",
			Service:     "temoin",
		}.Params(),
		State:  state,
		Result: result,
	}
}

// Le port écrit en base est celui que le script a lu sur la machine : openCloud
// n'a pas d'autre source.
func TestActionConcluded_WritesTheDomainWithThePortTheScriptRead(t *testing.T) {
	recorder, recorded := newRecorder("étape: poser le fragment", "info: port=8080", "résultat: fait")

	recorder.ActionConcluded(context.Background(), publication(store.StateApplied, "fait"))

	if len(recorded.recorded) != 1 {
		t.Fatalf("écrit %v, attendu une ligne", recorded.recorded)
	}
	written := recorded.recorded[0]
	if written.Name != "temoin.exemple.fr" || written.MachineID != "local" {
		t.Errorf("ligne = %+v", written)
	}
	if written.Environment != "prod" || written.Service != "temoin" {
		t.Errorf("ligne = %+v", written)
	}
	if written.Port != 8080 {
		t.Errorf("port = %d, attendu 8080", written.Port)
	}
}

// « inchangé » dit la même chose que « fait » : la machine porte le nom.
func TestActionConcluded_WritesTheDomainWhenNothingChanged(t *testing.T) {
	recorder, recorded := newRecorder("info: port=80", "résultat: inchangé")

	recorder.ActionConcluded(context.Background(), publication(store.StateApplied, "inchangé"))

	if len(recorded.recorded) != 1 {
		t.Fatalf("écrit %v, attendu une ligne", recorded.recorded)
	}
}

// Une action qui n'a pas abouti n'a rien publié : la table dit ce que la
// machine porte, pas ce qu'on a demandé.
func TestActionConcluded_WritesNothingWhenTheActionDidNotSucceed(t *testing.T) {
	for _, state := range []store.ActionState{store.StateFailed, store.StateRefused, store.StateRunning} {
		recorder, recorded := newRecorder("info: port=8080")

		recorder.ActionConcluded(context.Background(), publication(state, ""))

		if len(recorded.recorded) != 0 {
			t.Errorf("état %q : écrit %v, attendu rien", state, recorded.recorded)
		}
	}
}

// Sans port dans la sortie, il n'y a rien d'honnête à écrire.
func TestActionConcluded_WritesNothingWithoutAnObservedPort(t *testing.T) {
	recorder, recorded := newRecorder("étape: poser le fragment", "résultat: fait")

	recorder.ActionConcluded(context.Background(), publication(store.StateApplied, "fait"))

	if len(recorded.recorded) != 0 {
		t.Errorf("écrit %v, attendu rien", recorded.recorded)
	}
}

func TestActionConcluded_ForgetsTheDomainWhenTheRemovalSucceeds(t *testing.T) {
	recorder, recorded := newRecorder("étape: retirer le fragment", "résultat: fait")

	action := publication(store.StateApplied, "fait")
	action.Kind = string(catalog.KindVhostRemove)
	recorder.ActionConcluded(context.Background(), action)

	if len(recorded.forgotten) != 1 || recorded.forgotten[0] != "temoin.exemple.fr" {
		t.Errorf("retiré %v, attendu temoin.exemple.fr", recorded.forgotten)
	}
}

// Les autres actions ne disent rien des domaines : le recorder les ignore.
func TestActionConcluded_IgnoresTheOtherActions(t *testing.T) {
	recorder, recorded := newRecorder("info: port=8080")

	action := publication(store.StateApplied, "fait")
	action.Kind = string(catalog.KindProxy)
	recorder.ActionConcluded(context.Background(), action)

	if len(recorded.recorded) != 0 || len(recorded.forgotten) != 0 {
		t.Errorf("écrit %v, retiré %v, attendu rien", recorded.recorded, recorded.forgotten)
	}
}
