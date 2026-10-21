package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/ident"
)

// AuthorizeRequest est ce qu'un client apporte à /oauth/authorize. Scope
// est la liste brute, séparée par des espaces (RFC 6749 §3.3).
type AuthorizeRequest struct {
	ResponseType    string
	ClientID        string
	RedirectURI     string
	State           string
	Challenge       string
	ChallengeMethod string
	Scope           string
}

// ExchangeRequest est l'échange d'un code contre une paire de jetons.
type ExchangeRequest struct {
	ClientID     string
	ClientSecret string
	Code         string
	RedirectURI  string
	Verifier     string
}

// RefreshRequest est la rotation d'un jeton de rafraîchissement.
type RefreshRequest struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
}

// RedirectAllowed dit si le serveur peut rediriger vers cette URI : c'est
// à vérifier avant tout redirect, y compris pour dire une erreur.
func (s *Service) RedirectAllowed(ctx context.Context, uri string) (bool, error) {
	client, enabled, err := s.Client(ctx)
	if err != nil || !enabled {
		return false, err
	}
	return redirectAllowed(uri, client.RedirectURIs), nil
}

// validateAuthorize juge une demande d'autorisation, dans l'ordre que le
// serveur répète : l'URI de retour d'abord, puisque c'est par elle que
// partent les autres erreurs.
func (s *Service) validateAuthorize(ctx context.Context, request AuthorizeRequest) error {
	client, enabled, err := s.Client(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	if !redirectAllowed(request.RedirectURI, client.RedirectURIs) {
		return ErrRedirectURIInvalid
	}
	if request.ResponseType != "code" {
		return ErrResponseTypeInvalid
	}
	if request.ClientID != client.ID {
		return ErrClientUnknown
	}
	if request.Challenge == "" || request.ChallengeMethod != ChallengeMethod {
		return ErrChallengeInvalid
	}
	return nil
}

// issueCode tire le code d'une demande consentie, à la portée accordée.
func (s *Service) issueCode(ctx context.Context, consent Consent, scope Scope) (string, error) {
	code, err := newSecret(codeScheme)
	if err != nil {
		return "", fmt.Errorf("generate authorization code: %w", err)
	}
	now := s.now()
	err = s.store.InsertMCPCode(ctx, Code{
		Hash: code.hash, ClientID: consent.ClientID, RedirectURI: consent.RedirectURI, Challenge: consent.Challenge,
		Scope: scope, ExpiresAt: now.Add(CodeTTL), CreatedAt: now,
	})
	if err != nil {
		return "", err
	}
	return code.cleartext, nil
}

// Exchange consomme le code, vérifie le client et PKCE, ouvre une famille.
func (s *Service) Exchange(ctx context.Context, request ExchangeRequest) (Grant, error) {
	if err := s.checkClient(ctx, request.ClientID, request.ClientSecret); err != nil {
		return Grant{}, err
	}
	code, err := s.store.ConsumeMCPCode(ctx, HashSecret(request.Code), s.now())
	if err != nil {
		return Grant{}, err
	}
	if code.ClientID != request.ClientID || code.RedirectURI != request.RedirectURI {
		return Grant{}, ErrCodeInvalid
	}
	if request.Verifier == "" || !verifierMatches(request.Verifier, code.Challenge) {
		return Grant{}, ErrVerifierInvalid
	}
	familyID, err := ident.New()
	if err != nil {
		return Grant{}, fmt.Errorf("generate family id: %w", err)
	}
	s.logger.Info("mcp session opened", "family_id", familyID, "scope", string(code.Scope))
	return s.issue(ctx, familyID, code.Scope)
}

// Refresh consomme le jeton de rafraîchissement et en rend une nouvelle
// paire de la même famille. Un jeton présenté deux fois révoque toute la
// famille : le voleur est coupé aussi, qu'il ait rafraîchi le premier ou non.
func (s *Service) Refresh(ctx context.Context, request RefreshRequest) (Grant, error) {
	if err := s.checkClient(ctx, request.ClientID, request.ClientSecret); err != nil {
		return Grant{}, err
	}
	now := s.now()
	token, err := s.store.ConsumeMCPRefreshToken(ctx, HashSecret(request.RefreshToken), now)
	if errors.Is(err, ErrTokenReplayed) {
		s.logger.Warn("mcp refresh token replayed, family revoked", "family_id", token.FamilyID)
		// Une famille déjà tombée n'a plus rien à révoquer : le rejeu reste un rejeu.
		if err := s.store.RevokeMCPFamily(ctx, token.FamilyID, now); err != nil && !errors.Is(err, ErrNotFound) {
			return Grant{}, err
		}
		return Grant{}, ErrTokenReplayed
	}
	if err != nil {
		return Grant{}, err
	}
	s.logger.Info("mcp session refreshed", "family_id", token.FamilyID)
	// La famille garde la portée consentie à son ouverture.
	return s.issue(ctx, token.FamilyID, token.Scope)
}

// Verify accepte un jeton d'accès ou d'API vivant, et note son usage.
func (s *Service) Verify(ctx context.Context, bearer string) (Token, error) {
	if _, enabled, err := s.Client(ctx); err != nil {
		return Token{}, err
	} else if !enabled {
		return Token{}, ErrDisabled
	}
	token, err := s.store.GetMCPToken(ctx, HashSecret(bearer))
	if errors.Is(err, ErrNotFound) {
		return Token{}, ErrTokenInvalid
	}
	if err != nil {
		return Token{}, err
	}
	now := s.now()
	if token.Kind == KindRefresh || token.IsRevoked() || token.IsExpired(now) {
		return Token{}, ErrTokenInvalid
	}
	if err := s.store.TouchMCPToken(ctx, token.ID, now); err != nil {
		return Token{}, err
	}
	token.LastUsedAt = now
	return token, nil
}

func (s *Service) checkClient(ctx context.Context, clientID, secret string) error {
	client, enabled, err := s.Client(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	if clientID != client.ID || !secretMatches(secret, client.SecretHash) {
		return ErrSecretInvalid
	}
	return nil
}

func (s *Service) issue(ctx context.Context, familyID string, scope Scope) (Grant, error) {
	now := s.now()
	access, err := s.insertToken(ctx, accessScheme, KindAccess, familyID, scope, now, now.Add(AccessTTL))
	if err != nil {
		return Grant{}, err
	}
	refresh, err := s.insertToken(ctx, refreshScheme, KindRefresh, familyID, scope, now, now.Add(RefreshTTL))
	if err != nil {
		return Grant{}, err
	}
	return Grant{AccessToken: access, RefreshToken: refresh, ExpiresIn: int(AccessTTL.Seconds())}, nil
}

func (s *Service) insertToken(ctx context.Context, scheme string, kind Kind, familyID string, scope Scope, now, expiresAt time.Time) (string, error) {
	secret, err := newSecret(scheme)
	if err != nil {
		return "", fmt.Errorf("generate %s token: %w", kind, err)
	}
	token := Token{ID: secret.id, Hash: secret.hash, Prefix: secret.prefix, Kind: kind, Scope: scope, FamilyID: familyID, CreatedAt: now, ExpiresAt: expiresAt}
	if err := s.store.InsertMCPToken(ctx, token); err != nil {
		return "", err
	}
	return secret.cleartext, nil
}
