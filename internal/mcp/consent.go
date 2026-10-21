package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// Le consentement : /oauth/authorize ne rend plus de code, il met la demande
// en attente et l'opérateur répond sur une page. La demande vit en mémoire,
// dix minutes, sous un identifiant opaque, le seul que le navigateur porte :
// le client, l'URI de retour, le state et le défi ne reviennent jamais de la
// page. Un redémarrage l'oublie, et le client n'a qu'à relancer sa
// connexion : rien n'est accordé avant le oui.
const (
	ConsentTTL         = 10 * time.Minute
	MaxPendingConsents = 32
	consentIDBytes     = 16
)

// Consent est une demande d'autorisation qui attend la réponse de
// l'opérateur.
type Consent struct {
	ID             string
	ClientID       string
	RedirectURI    string
	State          string
	Challenge      string
	WriteRequested bool
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// pendingConsents borne les demandes en attente : au-delà, la plus ancienne
// cède sa place. Seul l'opérateur en crée, derrière son authentification.
type pendingConsents struct {
	mu   sync.Mutex
	byID map[string]Consent
}

func newPendingConsents() *pendingConsents {
	return &pendingConsents{byID: map[string]Consent{}}
}

func (p *pendingConsents) add(consent Consent, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dropExpired(now)
	if len(p.byID) >= MaxPendingConsents {
		delete(p.byID, p.oldest())
	}
	p.byID[consent.ID] = consent
}

func (p *pendingConsents) get(id string, now time.Time) (Consent, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	consent, ok := p.byID[id]
	if !ok || !now.Before(consent.ExpiresAt) {
		return Consent{}, false
	}
	return consent, true
}

// take rend la demande et l'oublie : elle ne reçoit qu'une réponse.
func (p *pendingConsents) take(id string, now time.Time) (Consent, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	consent, ok := p.byID[id]
	delete(p.byID, id)
	if !ok || !now.Before(consent.ExpiresAt) {
		return Consent{}, false
	}
	return consent, true
}

func (p *pendingConsents) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.byID)
}

func (p *pendingConsents) dropExpired(now time.Time) {
	for id, consent := range p.byID {
		if !now.Before(consent.ExpiresAt) {
			delete(p.byID, id)
		}
	}
}

func (p *pendingConsents) oldest() string {
	oldestID := ""
	var oldestAt time.Time
	for id, consent := range p.byID {
		if oldestID == "" || consent.CreatedAt.Before(oldestAt) {
			oldestID, oldestAt = id, consent.CreatedAt
		}
	}
	return oldestID
}

// requestsWrite dit si la liste de portées demande l'écriture. Une portée
// inconnue est ignorée (RFC 6749 §3.3 laisse le serveur en accorder moins) :
// un client qui demande « mcp » ou rien reçoit la lecture.
func requestsWrite(scope string) bool {
	return slices.Contains(strings.Fields(scope), string(ScopeWrite))
}

// grantedScope : l'écriture seulement si le client l'a demandée et que
// l'opérateur l'a cochée ; la lecture sinon.
func grantedScope(consent Consent, allowWrite bool) Scope {
	if consent.WriteRequested && allowWrite {
		return ScopeWrite
	}
	return ScopeRead
}

func newConsentID() (string, error) {
	raw := make([]byte, consentIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// RequestConsent valide la demande d'un client et la met en attente de
// l'opérateur. Aucun code n'existe encore.
func (s *Service) RequestConsent(ctx context.Context, request AuthorizeRequest) (Consent, error) {
	if err := s.validateAuthorize(ctx, request); err != nil {
		return Consent{}, err
	}
	id, err := newConsentID()
	if err != nil {
		return Consent{}, fmt.Errorf("generate consent id: %w", err)
	}
	now := s.now()
	consent := Consent{
		ID: id, ClientID: request.ClientID, RedirectURI: request.RedirectURI, State: request.State,
		Challenge: request.Challenge, WriteRequested: requestsWrite(request.Scope),
		CreatedAt: now, ExpiresAt: now.Add(ConsentTTL),
	}
	s.consents.add(consent, now)
	s.logger.Info("mcp authorization awaiting consent")
	return consent, nil
}

// PendingConsent rend la demande que la page montre.
func (s *Service) PendingConsent(ctx context.Context, id string) (Consent, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return Consent{}, err
	}
	consent, ok := s.consents.get(id, s.now())
	if !ok {
		return Consent{}, ErrConsentUnknown
	}
	return consent, nil
}

// ApproveConsent : l'opérateur a dit oui. La demande est consommée, puis
// rejugée, car l'activation et les adresses déclarées ont pu changer pendant
// que la page attendait ; le code porte la portée accordée.
func (s *Service) ApproveConsent(ctx context.Context, id string, allowWrite bool) (Consent, string, error) {
	consent, err := s.answerConsent(ctx, id)
	if err != nil {
		return Consent{}, "", err
	}
	scope := grantedScope(consent, allowWrite)
	code, err := s.issueCode(ctx, consent, scope)
	if err != nil {
		return Consent{}, "", err
	}
	s.logger.Info("mcp authorization code issued", "scope", string(scope))
	s.auditor.Record(ctx, consentEvent(audit.MCPSessionAuthorize, consent, scope))
	return consent, code, nil
}

// DenyConsent : l'opérateur a dit non. Le client recevra access_denied.
func (s *Service) DenyConsent(ctx context.Context, id string) (Consent, error) {
	consent, err := s.answerConsent(ctx, id)
	if err != nil {
		return Consent{}, err
	}
	s.logger.Info("mcp authorization denied")
	s.auditor.Record(ctx, consentEvent(audit.MCPSessionDeny, consent, requestedScope(consent)))
	return consent, nil
}

// answerConsent consomme la demande et vérifie que l'URI de retour reste
// acceptée : la réponse, oui ou non, part vers elle.
func (s *Service) answerConsent(ctx context.Context, id string) (Consent, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return Consent{}, err
	}
	consent, ok := s.consents.take(id, s.now())
	if !ok {
		return Consent{}, ErrConsentUnknown
	}
	allowed, err := s.RedirectAllowed(ctx, consent.RedirectURI)
	if err != nil {
		return Consent{}, err
	}
	if !allowed {
		return Consent{}, ErrRedirectURIInvalid
	}
	return consent, nil
}

func requestedScope(consent Consent) Scope {
	if consent.WriteRequested {
		return ScopeWrite
	}
	return ScopeRead
}

func (s *Service) requireEnabled(ctx context.Context) error {
	_, enabled, err := s.Client(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	return nil
}
