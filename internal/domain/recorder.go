package domain

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// Store est ce que domain écrit et relit. Le vrai est *store.Store.
type Store interface {
	RecordDomain(ctx context.Context, domain store.Domain) error
	ForgetDomain(ctx context.Context, name string) error
	Lines(ctx context.Context, actionID string, afterSeq int64) ([]store.ActionLine, error)
}

// La clé que le script écrit pour le port qu'il a lu dans la définition du
// service : « info: port=8080 ».
const portFact = "port"

// Recorder suit les conclusions du runner et n'en retient que les deux
// actions d'hôte virtuel.
type Recorder struct {
	store  Store
	logger *slog.Logger
}

func New(database Store, logger *slog.Logger) *Recorder {
	return &Recorder{store: database, logger: logger}
}

// ActionConcluded est appelé par le runner à chaque conclusion. Rien n'est
// écrit tant que l'action n'a pas abouti : « fait » et « inchangé » disent
// tous deux que la machine porte le nom, un échec et un refus ne disent rien.
func (r *Recorder) ActionConcluded(ctx context.Context, action store.Action) {
	if action.State != store.StateApplied {
		return
	}

	switch catalog.Kind(action.Kind) {
	case catalog.KindVhost:
		r.record(ctx, action)
	case catalog.KindVhostRemove:
		r.forget(ctx, action)
	}
}

func (r *Recorder) record(ctx context.Context, action store.Action) {
	port, found := r.observedPort(ctx, action)
	if !found {
		r.logger.Warn("published domain without a port in the output", "action_id", action.ID)
		return
	}

	publication := catalog.VhostPublicationOf(action.Params)
	published := store.Domain{
		Name:        publication.Domain,
		MachineID:   action.MachineID,
		Environment: publication.Environment,
		Service:     publication.Service,
		Port:        port,
	}
	if err := r.store.RecordDomain(ctx, published); err != nil {
		r.logger.Error("record domain", "action_id", action.ID, "domain", published.Name, "error", err)
		return
	}
	r.logger.Info("domain published", "action_id", action.ID, "domain", published.Name, "port", port)
}

func (r *Recorder) forget(ctx context.Context, action store.Action) {
	name := catalog.VhostPublicationOf(action.Params).Domain
	if err := r.store.ForgetDomain(ctx, name); err != nil {
		r.logger.Error("forget domain", "action_id", action.ID, "domain", name, "error", err)
		return
	}
	r.logger.Info("domain withdrawn", "action_id", action.ID, "domain", name)
}

// observedPort relit le port dans la sortie du script : c'est lui qui l'a lu
// sur la machine, et openCloud n'a pas d'autre source.
func (r *Recorder) observedPort(ctx context.Context, action store.Action) (int, bool) {
	lines, err := r.store.Lines(ctx, action.ID, 0)
	if err != nil {
		r.logger.Error("read action lines", "action_id", action.ID, "error", err)
		return 0, false
	}

	// La dernière valeur vue gagne : une action rejouée réécrit ses constats.
	port := 0
	found := false
	for _, line := range lines {
		fact, isFact := strings.CutPrefix(strings.TrimSpace(line.Text), catalog.InfoPrefix)
		if !isFact {
			continue
		}
		key, value, hasValue := strings.Cut(strings.TrimSpace(fact), "=")
		if !hasValue || key != portFact {
			continue
		}
		number, err := strconv.Atoi(value)
		if err != nil || number < 1 || number > 65535 {
			continue
		}
		port, found = number, true
	}
	return port, found
}
