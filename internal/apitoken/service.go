package apitoken

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// Ce que le service attend du store.
type Store interface {
	InsertAPIToken(ctx context.Context, token Token) error
	GetAPIToken(ctx context.Context, hash string) (Token, error)
	ListAPITokens(ctx context.Context) ([]Token, error)
	TouchAPIToken(ctx context.Context, id string, at time.Time) error
	DeleteAPIToken(ctx context.Context, id string) error
	DeleteAllAPITokens(ctx context.Context, now time.Time) (int, error)
	PurgeExpiredAPITokens(ctx context.Context, before time.Time) error
}

type Service struct {
	store   Store
	auditor *audit.Recorder
	logger  *slog.Logger
	now     func() time.Time
	// creating tient compter-puis-insérer d'un seul tenant : deux créations
	// simultanées ne passent pas toutes deux sur la dernière place.
	creating sync.Mutex
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

// Create tire un jeton de la portée et de la durée demandées, rendu en
// clair une fois.
func (s *Service) Create(ctx context.Context, request Request) (string, Token, error) {
	if err := ValidateName(request.Name); err != nil {
		return "", Token{}, err
	}
	scope, err := scopeOf(request.Scope)
	if err != nil {
		return "", Token{}, err
	}
	now := s.now()
	expiresAt, err := expiryOf(request.Days, now)
	if err != nil {
		return "", Token{}, err
	}
	secret, err := newSecret()
	if err != nil {
		return "", Token{}, fmt.Errorf("generate api token: %w", err)
	}
	token := Token{
		ID: secret.id, Hash: secret.hash, Prefix: secret.prefix, Scope: scope,
		Name: strings.TrimSpace(request.Name), CreatedAt: now, ExpiresAt: expiresAt,
	}
	if err := s.insertUnderCeiling(ctx, token); err != nil {
		return "", Token{}, err
	}
	s.logger.Info("api token created", "token_id", token.ID, "name", token.Name, "scope", string(scope), "expires_at", expiresAt)
	s.auditor.Record(ctx, createEvent(token))
	return secret.cleartext, token, nil
}

func (s *Service) insertUnderCeiling(ctx context.Context, token Token) error {
	s.creating.Lock()
	defer s.creating.Unlock()
	existing, err := s.List(ctx)
	if err != nil {
		return err
	}
	if len(existing) >= MaxTokens {
		return ErrTooMany
	}
	return s.store.InsertAPIToken(ctx, token)
}

// List rend les jetons vivants, les plus récents d'abord. Un jeton échu que
// la purge n'a pas encore effacé n'y figure plus.
func (s *Service) List(ctx context.Context) ([]Token, error) {
	tokens, err := s.store.ListAPITokens(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	return slices.DeleteFunc(tokens, func(token Token) bool { return token.IsExpired(now) }), nil
}

// Revoke efface le jeton : présenté ensuite, il est inconnu, comme un faux.
func (s *Service) Revoke(ctx context.Context, id string) error {
	if err := s.store.DeleteAPIToken(ctx, id); err != nil {
		return err
	}
	s.logger.Info("api token revoked", "token_id", id)
	s.auditor.Record(ctx, audit.Event{Action: audit.APITokenRevoke, ObjectID: id})
	return nil
}

// RevokeAll efface tous les jetons et dit combien. Un compte créé ou
// recréé n'a pas à hériter des jetons de l'époque d'avant.
func (s *Service) RevokeAll(ctx context.Context) (int, error) {
	revoked, err := s.store.DeleteAllAPITokens(ctx, s.now())
	if err != nil {
		return 0, err
	}
	s.logger.Warn("api tokens revoked", "api_tokens", revoked)
	s.auditor.Record(ctx, audit.Event{
		Action:  audit.APITokenRevoke,
		Details: audit.Details{"all": "true", "api_tokens": strconv.Itoa(revoked)},
	})
	return revoked, nil
}

// Verify accepte un jeton vivant et note son usage, au plus une fois par
// minute : un client peut enchaîner les lectures.
func (s *Service) Verify(ctx context.Context, bearer string) (Token, error) {
	token, err := s.store.GetAPIToken(ctx, HashSecret(bearer))
	if errors.Is(err, ErrNotFound) {
		return Token{}, ErrInvalid
	}
	if err != nil {
		return Token{}, err
	}
	now := s.now()
	if token.IsExpired(now) {
		return Token{}, ErrInvalid
	}
	if token.LastUsedAt.IsZero() || now.Sub(token.LastUsedAt) >= time.Minute {
		if err := s.store.TouchAPIToken(ctx, token.ID, now); err != nil {
			return Token{}, err
		}
		token.LastUsedAt = now
	}
	return token, nil
}

// Purge efface les jetons échus.
func (s *Service) Purge(ctx context.Context) error {
	return s.store.PurgeExpiredAPITokens(ctx, s.now())
}

// Watch purge à cadence fixe jusqu'à l'arrêt.
func (s *Service) Watch(ctx context.Context) {
	ticker := time.NewTicker(PurgeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Purge(ctx); err != nil && ctx.Err() == nil {
				s.logger.Error("purge api tokens", "error", err)
			}
		}
	}
}

// createEvent dit le nom, la portée et l'échéance ; ni le jeton ni son
// préfixe.
func createEvent(created Token) audit.Event {
	expires := "never"
	if !created.ExpiresAt.IsZero() {
		expires = created.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return audit.Event{
		Action: audit.APITokenCreate, ObjectID: created.ID, ObjectName: created.Name,
		Details: audit.Details{"scope": string(created.Scope), "expires_at": expires},
	}
}
