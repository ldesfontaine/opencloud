package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Ce que le service attend du store.
type Store interface {
	GetMCPClient(ctx context.Context) (Client, error)
	InsertMCPClient(ctx context.Context, client Client) error
	UpdateMCPClientSecret(ctx context.Context, hash, prefix string) error
	UpdateMCPClientRedirectURIs(ctx context.Context, uris []string) error
	DeleteMCPClient(ctx context.Context) error

	InsertMCPCode(ctx context.Context, code Code) error
	ConsumeMCPCode(ctx context.Context, hash string, now time.Time) (Code, error)

	InsertMCPToken(ctx context.Context, token Token) error
	GetMCPToken(ctx context.Context, hash string) (Token, error)
	ConsumeMCPRefreshToken(ctx context.Context, hash string, now time.Time) (Token, error)
	TouchMCPToken(ctx context.Context, id string, at time.Time) error
	RevokeMCPToken(ctx context.Context, id string, at time.Time) error
	RevokeMCPFamily(ctx context.Context, familyID string, at time.Time) error
	RevokeMCPOAuthTokens(ctx context.Context, at time.Time) error
	DeleteAllMCPTokens(ctx context.Context) error
	ListMCPTokens(ctx context.Context, kind Kind) ([]Token, error)
	ListMCPSessions(ctx context.Context, now time.Time) ([]Session, error)
	PurgeMCPExpired(ctx context.Context, before time.Time) error
}

type Service struct {
	store  Store
	logger *slog.Logger
	now    func() time.Time
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger, now: time.Now}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// Client rend le client OAuth, et faux quand MCP n'est pas activé.
func (s *Service) Client(ctx context.Context) (Client, bool, error) {
	client, err := s.store.GetMCPClient(ctx)
	if errors.Is(err, ErrNotFound) {
		return Client{}, false, nil
	}
	if err != nil {
		return Client{}, false, err
	}
	return client, true, nil
}

// Enable crée le client et tire son secret, rendu en clair une seule fois.
// Activer, c'est avoir un secret : il n'existe pas d'état activé sans.
func (s *Service) Enable(ctx context.Context) (string, Client, error) {
	if _, enabled, err := s.Client(ctx); err != nil {
		return "", Client{}, err
	} else if enabled {
		return "", Client{}, ErrAlreadyEnabled
	}
	secret, err := newSecret(secretScheme)
	if err != nil {
		return "", Client{}, fmt.Errorf("generate client secret: %w", err)
	}
	client := Client{ID: ClientID, SecretHash: secret.hash, SecretPrefix: secret.prefix, CreatedAt: s.now()}
	if err := s.store.InsertMCPClient(ctx, client); err != nil {
		return "", Client{}, err
	}
	s.logger.Info("mcp enabled", "client_id", ClientID)
	return secret.cleartext, client, nil
}

// Disable retire le client et tous les jetons : plus rien n'entre, et il
// n'y a plus rien à rejouer.
func (s *Service) Disable(ctx context.Context) error {
	if err := s.store.DeleteMCPClient(ctx); err != nil {
		return err
	}
	if err := s.store.DeleteAllMCPTokens(ctx); err != nil {
		return err
	}
	s.logger.Info("mcp disabled")
	return nil
}

// RegenerateSecret remplace le secret et révoque les autorisations OAuth
// obtenues avec l'ancien ; les jetons d'API, indépendants, restent.
func (s *Service) RegenerateSecret(ctx context.Context) (string, error) {
	if _, err := s.store.GetMCPClient(ctx); err != nil {
		return "", err
	}
	secret, err := newSecret(secretScheme)
	if err != nil {
		return "", fmt.Errorf("generate client secret: %w", err)
	}
	if err := s.store.UpdateMCPClientSecret(ctx, secret.hash, secret.prefix); err != nil {
		return "", err
	}
	if err := s.store.RevokeMCPOAuthTokens(ctx, s.now()); err != nil {
		return "", err
	}
	s.logger.Info("mcp client secret regenerated")
	return secret.cleartext, nil
}

// SetRedirectURIs remplace la liste déclarée.
func (s *Service) SetRedirectURIs(ctx context.Context, uris []string) (Client, error) {
	cleaned, err := ValidateRedirectURIs(uris)
	if err != nil {
		return Client{}, err
	}
	if err := s.store.UpdateMCPClientRedirectURIs(ctx, cleaned); err != nil {
		return Client{}, err
	}
	return s.store.GetMCPClient(ctx)
}

// CreateAPIToken tire un jeton sans échéance, rendu en clair une fois.
func (s *Service) CreateAPIToken(ctx context.Context, name string) (string, Token, error) {
	if _, enabled, err := s.Client(ctx); err != nil {
		return "", Token{}, err
	} else if !enabled {
		return "", Token{}, ErrDisabled
	}
	if err := ValidateName(name); err != nil {
		return "", Token{}, err
	}
	existing, err := s.store.ListMCPTokens(ctx, KindAPI)
	if err != nil {
		return "", Token{}, err
	}
	if len(existing) >= MaxAPITokens {
		return "", Token{}, ErrTooManyTokens
	}
	secret, err := newSecret(apiScheme)
	if err != nil {
		return "", Token{}, fmt.Errorf("generate api token: %w", err)
	}
	token := Token{ID: secret.id, Hash: secret.hash, Prefix: secret.prefix, Kind: KindAPI, Name: strings.TrimSpace(name), CreatedAt: s.now()}
	if err := s.store.InsertMCPToken(ctx, token); err != nil {
		return "", Token{}, err
	}
	s.logger.Info("mcp api token created", "token_id", token.ID, "name", token.Name)
	return secret.cleartext, token, nil
}

// APITokens liste les jetons d'API vivants, les plus récents d'abord.
func (s *Service) APITokens(ctx context.Context) ([]Token, error) {
	return s.store.ListMCPTokens(ctx, KindAPI)
}

func (s *Service) RevokeToken(ctx context.Context, id string) error {
	if err := s.store.RevokeMCPToken(ctx, id, s.now()); err != nil {
		return err
	}
	s.logger.Info("mcp token revoked", "token_id", id)
	return nil
}

// Sessions liste les autorisations OAuth vivantes.
func (s *Service) Sessions(ctx context.Context) ([]Session, error) {
	return s.store.ListMCPSessions(ctx, s.now())
}

func (s *Service) RevokeSession(ctx context.Context, familyID string) error {
	if err := s.store.RevokeMCPFamily(ctx, familyID, s.now()); err != nil {
		return err
	}
	s.logger.Info("mcp session revoked", "family_id", familyID)
	return nil
}

// Purge efface les codes et jetons échus.
func (s *Service) Purge(ctx context.Context) error {
	return s.store.PurgeMCPExpired(ctx, s.now())
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
				s.logger.Error("purge mcp tokens", "error", err)
			}
		}
	}
}
