package verbrequest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/ident"
)

// Ce que le service attend du store.
type Store interface {
	InsertVerbRequest(ctx context.Context, request Request) error
	// FindPendingVerbRequest rend la demande en attente du même jeton, pour le
	// même verbe, les mêmes paramètres et le même corps ; ErrNotFound sinon.
	FindPendingVerbRequest(ctx context.Context, request Request, now time.Time) (Request, error)
	CountPendingVerbRequests(ctx context.Context, tokenID string, now time.Time) (int, error)
	ListPendingVerbRequests(ctx context.Context, now time.Time) ([]Request, error)
	GetVerbRequest(ctx context.Context, id string) (Request, error)
	// DecideVerbRequest passe une demande en attente et non échue à l'état
	// donné, d'un seul geste : deux décisions simultanées n'en gagnent qu'une.
	// ErrNotFound si aucune demande ne remplit ces conditions.
	DecideVerbRequest(ctx context.Context, id string, state State, now time.Time) (Request, error)
	// ExpireDueVerbRequests échoit les demandes en attente dont l'échéance est
	// passée et les rend.
	ExpireDueVerbRequests(ctx context.Context, now time.Time) ([]Request, error)
	PurgeVerbRequests(ctx context.Context, before time.Time) error
}

// Watcher apprend qu'une demande est apparue, décidée ou échue.
type Watcher interface {
	VerbRequestsChanged()
}

type Service struct {
	store   Store
	auditor *audit.Recorder
	watcher Watcher
	logger  *slog.Logger
	now     func() time.Time
	// submitting tient chercher-compter-insérer d'un seul tenant : deux
	// demandes simultanées ne passent pas toutes deux sous le plafond.
	submitting sync.Mutex
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger, now: time.Now}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// SetAuditor branche le journal d'audit ; nil est toléré.
func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

// SetWatcher branche l'annonce des changements ; nil est toléré.
func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

// Submission est ce que le jeton demande : le verbe, l'objet visé, ses
// paramètres de chemin et son corps. ObjectName est le nom de l'objet à
// l'instant de la demande.
type Submission struct {
	Verb       string
	ObjectName string
	Params     map[string]string
	Body       string
	TokenID    string
	TokenName  string
	OnBehalfOf string
}

// Submit dépose la demande et rend son état « en attente ». Le même jeton qui
// redemande la même chose pendant qu'elle attend retrouve la même demande :
// un nouvel essai n'ajoute rien à l'écran de l'opérateur.
func (s *Service) Submit(ctx context.Context, submission Submission) (Request, error) {
	id, err := ident.New()
	if err != nil {
		return Request{}, fmt.Errorf("draw verb request id: %w", err)
	}
	now := s.now()
	request := Request{
		ID: id, Verb: submission.Verb, ObjectName: submission.ObjectName, Params: submission.Params, Body: submission.Body,
		TokenID: submission.TokenID, TokenName: submission.TokenName, OnBehalfOf: submission.OnBehalfOf,
		State: StateAwaiting, CreatedAt: now, ExpiresAt: now.Add(TTL),
	}
	s.Sweep(ctx)
	stored, created, err := s.insertUnderCeiling(ctx, request, now)
	if err != nil || !created {
		return stored, err
	}
	s.logger.Info("verb request submitted", "request_id", request.ID, "verb", request.Verb, "token_id", request.TokenID)
	s.auditor.Record(ctx, requestEvent(audit.VerbRequest, request))
	s.announce()
	return request, nil
}

// insertUnderCeiling écrit la demande, sauf si la même attend déjà (rendue à
// sa place, created faux) ou si le jeton a atteint son plafond.
func (s *Service) insertUnderCeiling(ctx context.Context, request Request, now time.Time) (stored Request, created bool, err error) {
	s.submitting.Lock()
	defer s.submitting.Unlock()
	waiting, err := s.store.FindPendingVerbRequest(ctx, request, now)
	if err == nil {
		return waiting, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Request{}, false, err
	}
	pending, err := s.store.CountPendingVerbRequests(ctx, request.TokenID, now)
	if err != nil {
		return Request{}, false, err
	}
	if pending >= MaxPendingPerToken {
		return Request{}, false, ErrTooMany
	}
	if err := s.store.InsertVerbRequest(ctx, request); err != nil {
		return Request{}, false, err
	}
	return request, true, nil
}

// ListPending rend les demandes qui attendent l'opérateur, les plus
// anciennes d'abord : la première à échoir est la première à traiter.
func (s *Service) ListPending(ctx context.Context) ([]Request, error) {
	s.Sweep(ctx)
	return s.store.ListPendingVerbRequests(ctx, s.now())
}

// Confirm prend la demande pour la rejouer : elle passe à « confirmée » avant
// que la route ne s'exécute, d'un seul geste, et une seconde confirmation
// reçoit ErrDecided. Le service ne rejoue rien lui-même.
func (s *Service) Confirm(ctx context.Context, id string) (Request, error) {
	return s.decide(ctx, id, StateConfirmed)
}

// Reject refuse la demande : rien n'est exécuté, l'audit garde le refus.
func (s *Service) Reject(ctx context.Context, id string) (Request, error) {
	request, err := s.decide(ctx, id, StateRejected)
	if err != nil {
		return Request{}, err
	}
	s.logger.Info("verb request rejected", "request_id", request.ID, "verb", request.Verb)
	s.auditor.Record(ctx, requestEvent(audit.VerbReject, request))
	return request, nil
}

func (s *Service) decide(ctx context.Context, id string, state State) (Request, error) {
	s.Sweep(ctx)
	request, err := s.store.DecideVerbRequest(ctx, id, state, s.now())
	if errors.Is(err, ErrNotFound) {
		return Request{}, s.whyNotDecidable(ctx, id)
	}
	if err != nil {
		return Request{}, err
	}
	s.announce()
	return request, nil
}

// whyNotDecidable dit pourquoi la demande ne se décide pas : elle n'existe
// pas, elle est échue, ou quelqu'un l'a déjà décidée.
func (s *Service) whyNotDecidable(ctx context.Context, id string) error {
	request, err := s.store.GetVerbRequest(ctx, id)
	if err != nil {
		return err
	}
	if request.State == StateExpired || (request.State == StateAwaiting && !request.ExpiresAt.After(s.now())) {
		return ErrExpired
	}
	return ErrDecided
}

// Sweep échoit les demandes en attente dont le délai est passé et journalise
// chaque échéance. Il passe avant toute lecture ou décision : une demande
// échue ne se confirme jamais, même si le passage de fond n'a pas encore tourné.
func (s *Service) Sweep(ctx context.Context) {
	expired, err := s.store.ExpireDueVerbRequests(ctx, s.now())
	if err != nil {
		s.logger.Error("expire verb requests", "error", err)
		return
	}
	if len(expired) == 0 {
		return
	}
	ruleCtx := audit.WithActor(ctx, audit.Rule("verb_requests"))
	for _, request := range expired {
		s.logger.Info("verb request expired", "request_id", request.ID, "verb", request.Verb)
		s.auditor.Record(ruleCtx, requestEvent(audit.VerbExpire, request))
	}
	s.announce()
}

// Watch échoit et purge à cadence fixe jusqu'à l'arrêt.
func (s *Service) Watch(ctx context.Context) {
	ticker := time.NewTicker(SweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sweep(ctx)
			if err := s.store.PurgeVerbRequests(ctx, s.now().Add(-Retention)); err != nil && ctx.Err() == nil {
				s.logger.Error("purge verb requests", "error", err)
			}
		}
	}
}

func (s *Service) announce() {
	if s.watcher != nil {
		s.watcher.VerbRequestsChanged()
	}
}

// requestEvent dit la demande au journal : le verbe et l'objet visé, jamais
// le corps ni les paramètres entiers.
func requestEvent(action audit.Action, request Request) audit.Event {
	return audit.Event{
		Action: action, ObjectID: request.ID, ObjectName: request.ObjectName,
		Details: audit.Details{"verb": request.Verb, "object_id": request.ObjectID()},
	}
}
