package mcp

import (
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// Le seul client OAuth : openCloud a un opérateur, pas des tenants.
	ClientID = "opencloud"
	// Un code d'autorisation vit dix minutes ; un accès une heure ; un
	// rafraîchissement trente jours. Un jeton d'API n'a pas d'échéance :
	// l'opérateur le révoque.
	CodeTTL    = 10 * time.Minute
	AccessTTL  = time.Hour
	RefreshTTL = 30 * 24 * time.Hour
	// La purge efface les codes et jetons échus, révoqués ou non.
	PurgeEvery = 15 * time.Minute

	MaxAPITokens       = 16
	MaxRedirectURIs    = 8
	MaxTokenNameLength = 80
	MaxRedirectURILen  = 512
	// La seule méthode PKCE acceptée.
	ChallengeMethod = "S256"
)

// Kind est la sorte d'un jeton.
type Kind string

const (
	KindAccess  Kind = "access"
	KindRefresh Kind = "refresh"
	KindAPI     Kind = "api"
)

func Kinds() []Kind {
	return []Kind{KindAccess, KindRefresh, KindAPI}
}

// Client est le client OAuth : il existe quand MCP est activé. Le secret
// n'est stocké que haché ; le préfixe sert à le reconnaître.
type Client struct {
	ID           string
	SecretHash   string
	SecretPrefix string
	RedirectURIs []string
	CreatedAt    time.Time
}

// Code est un code d'autorisation, stocké haché, consommé une fois.
type Code struct {
	Hash        string
	ClientID    string
	RedirectURI string
	Challenge   string
	ExpiresAt   time.Time
	Used        bool
	CreatedAt   time.Time
}

// Token est un jeton stocké haché. FamilyID lie les rotations d'une même
// autorisation OAuth ; vide pour un jeton d'API. ExpiresAt est zéro pour
// un jeton d'API.
type Token struct {
	ID         string
	Hash       string
	Prefix     string
	Kind       Kind
	Name       string
	FamilyID   string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
}

func (t Token) IsRevoked() bool {
	return !t.RevokedAt.IsZero()
}

func (t Token) IsExpired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt)
}

// Masked est tout ce qu'une page peut montrer d'un jeton ou d'un secret.
func (t Token) Masked() string {
	return t.Prefix + "…"
}

// Session est une autorisation OAuth vivante : la famille de jetons d'un
// client connecté, du premier échange au dernier rafraîchissement.
type Session struct {
	FamilyID   string
	StartedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
}

// Grant est ce que le point de jeton répond : les deux clairs, une fois.
type Grant struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

var (
	ErrDisabled            = errors.New("mcp: disabled")
	ErrAlreadyEnabled      = errors.New("mcp: already enabled")
	ErrNotFound            = errors.New("mcp: not found")
	ErrClientUnknown       = errors.New("mcp: client unknown")
	ErrSecretInvalid       = errors.New("mcp: client secret invalid")
	ErrResponseTypeInvalid = errors.New("mcp: response type invalid")
	ErrRedirectURIInvalid  = errors.New("mcp: redirect uri invalid")
	ErrChallengeInvalid    = errors.New("mcp: code challenge invalid")
	ErrCodeInvalid         = errors.New("mcp: authorization code invalid")
	ErrVerifierInvalid     = errors.New("mcp: code verifier invalid")
	ErrTokenInvalid        = errors.New("mcp: token invalid")
	ErrTokenReplayed       = errors.New("mcp: refresh token replayed")
	ErrNameInvalid         = errors.New("mcp: token name invalid")
	ErrTooManyTokens       = errors.New("mcp: too many api tokens")
	ErrTooManyRedirectURIs = errors.New("mcp: too many redirect uris")
)

// ValidateName borne le nom d'un jeton d'API : un mot pour le reconnaître.
func ValidateName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > MaxTokenNameLength {
		return ErrNameInvalid
	}
	return nil
}

// ValidateRedirectURIs vérifie la liste déclarée : des URL absolues en
// http ou https, avec un hôte, sans fragment, huit au plus. La boucle
// locale n'a pas besoin d'y figurer.
func ValidateRedirectURIs(uris []string) ([]string, error) {
	if len(uris) > MaxRedirectURIs {
		return nil, ErrTooManyRedirectURIs
	}
	cleaned := make([]string, 0, len(uris))
	for _, raw := range uris {
		uri := strings.TrimSpace(raw)
		if uri == "" {
			continue
		}
		if len(uri) > MaxRedirectURILen || !isAbsoluteHTTP(uri) {
			return nil, ErrRedirectURIInvalid
		}
		cleaned = append(cleaned, uri)
	}
	return cleaned, nil
}

func isAbsoluteHTTP(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Hostname() != "" && parsed.Fragment == ""
}

// redirectAllowed dit si un redirect_uri peut recevoir un code : la
// boucle locale toujours, n'importe quel port et chemin, c'est là qu'un
// client local écoute ; ailleurs, seulement une URI déclarée, comparée
// telle quelle (RFC 6749 §3.1.2.3).
func redirectAllowed(raw string, declared []string) bool {
	if !isAbsoluteHTTP(raw) {
		return false
	}
	parsed, _ := url.Parse(raw)
	if isLoopback(parsed.Hostname()) {
		return true
	}
	for _, uri := range declared {
		if uri == raw {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
