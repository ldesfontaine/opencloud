package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Coût bcrypt : environ 100 ms par vérification sur une machine modeste.
// La mémoire des identifiants vérifiés évite de le payer à chaque requête.
const (
	DefaultHashCost = 12
	rememberKeyLen  = 32
)

// Ce que le service attend du store.
type Store interface {
	GetAuthState(ctx context.Context) (State, error)
	SaveAuthState(ctx context.Context, state State, at time.Time) error
}

// remembered est l'empreinte des derniers identifiants vérifiés, liée à
// l'empreinte bcrypt du moment : un changement de mot de passe, une
// désactivation puis réactivation, même faits par la commande de secours
// dans un autre processus, la rendent caduque d'eux-mêmes.
type remembered struct {
	passwordHash string
	digest       []byte
}

type Service struct {
	store    Store
	logger   *slog.Logger
	now      func() time.Time
	hashCost int
	// Clé tirée au démarrage : l'empreinte en mémoire ne se rejoue pas
	// d'un processus à l'autre et ne se calcule pas hors de celui-ci.
	rememberKey []byte

	mu   sync.Mutex
	last remembered
	// Une vérification lente à la fois : les requêtes parallèles d'un
	// navigateur qui vient de saisir ses identifiants attendent la
	// première, puis passent par la mémoire. Borne aussi le processeur.
	verifying sync.Mutex
}

func New(store Store, logger *slog.Logger) (*Service, error) {
	key := make([]byte, rememberKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate remember key: %w", err)
	}
	return &Service{store: store, logger: logger, now: time.Now, hashCost: DefaultHashCost, rememberKey: key}, nil
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// SetHashCost sert aux tests, où bcrypt au coût réel coûterait des secondes.
func (s *Service) SetHashCost(cost int) {
	s.hashCost = cost
}

// State relit l'état dans la base à chaque appel : la commande de secours,
// qui écrit depuis un autre processus, prend effet sans redémarrer serve.
func (s *Service) State(ctx context.Context) (State, error) {
	return s.store.GetAuthState(ctx)
}

// EnableBasic choisit identifiant et mot de passe, et passe en mode basic.
func (s *Service) EnableBasic(ctx context.Context, username, password string) (State, error) {
	current, err := s.store.GetAuthState(ctx)
	if err != nil {
		return State{}, err
	}
	if current.Mode != ModeNone {
		return State{}, ErrAlreadyEnabled
	}
	if err := ValidateUsername(username); err != nil {
		return State{}, err
	}
	hash, err := s.hashPassword(password)
	if err != nil {
		return State{}, err
	}
	next := State{Mode: ModeBasic, Username: username, PasswordHash: hash, EnabledAt: s.now()}
	if err := s.store.SaveAuthState(ctx, next, s.now()); err != nil {
		return State{}, err
	}
	s.forget()
	s.logger.Info("interface authentication enabled", "mode", string(ModeBasic), "username", username)
	return next, nil
}

// EnableForward passe en mode forward : l'identité vient de l'en-tête
// que pose le mandataire. Le serveur vérifie d'abord que la requête qui
// active le porte déjà, sans quoi l'opérateur s'enfermerait dehors.
func (s *Service) EnableForward(ctx context.Context, header string) (State, error) {
	current, err := s.store.GetAuthState(ctx)
	if err != nil {
		return State{}, err
	}
	if current.Mode != ModeNone {
		return State{}, ErrAlreadyEnabled
	}
	if err := ValidateHeader(header); err != nil {
		return State{}, err
	}
	next := State{Mode: ModeForward, Header: header, EnabledAt: s.now()}
	if err := s.store.SaveAuthState(ctx, next, s.now()); err != nil {
		return State{}, err
	}
	s.forget()
	s.logger.Info("interface authentication enabled", "mode", string(ModeForward), "header", header)
	return next, nil
}

// ChangePassword garde l'identifiant et remplace l'empreinte.
func (s *Service) ChangePassword(ctx context.Context, password string) (State, error) {
	current, err := s.store.GetAuthState(ctx)
	if err != nil {
		return State{}, err
	}
	if current.Mode == ModeForward {
		return State{}, ErrNoPassword
	}
	if current.Mode != ModeBasic {
		return State{}, ErrDisabled
	}
	hash, err := s.hashPassword(password)
	if err != nil {
		return State{}, err
	}
	current.PasswordHash = hash
	if err := s.store.SaveAuthState(ctx, current, s.now()); err != nil {
		return State{}, err
	}
	s.forget()
	s.logger.Info("interface password changed", "username", current.Username)
	return current, nil
}

// Disable rouvre l'interface et efface identifiant, empreinte et en-tête.
func (s *Service) Disable(ctx context.Context) error {
	current, err := s.store.GetAuthState(ctx)
	if err != nil {
		return err
	}
	if current.Mode == ModeNone {
		return ErrDisabled
	}
	if err := s.store.SaveAuthState(ctx, State{Mode: ModeNone}, s.now()); err != nil {
		return err
	}
	s.forget()
	s.logger.Warn("interface authentication disabled", "previous_mode", string(current.Mode))
	return nil
}

// Remembered dit, sans hachage lent, si ces identifiants sont ceux déjà
// vérifiés contre l'empreinte en vigueur.
func (s *Service) Remembered(state State, username, password string) bool {
	digest := s.digestOf(username, password)
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	if last.passwordHash == "" || last.passwordHash != state.PasswordHash {
		return false
	}
	return hmac.Equal(digest, last.digest)
}

// Verify compare au prix de bcrypt, et retient les identifiants justes.
// L'identifiant est comparé en temps constant, le mot de passe par bcrypt
// même quand l'identifiant est faux : la durée ne dit pas lequel l'est.
func (s *Service) Verify(state State, username, password string) bool {
	if state.Mode != ModeBasic || state.PasswordHash == "" {
		return false
	}
	s.verifying.Lock()
	defer s.verifying.Unlock()
	if s.Remembered(state, username, password) {
		return true
	}
	usernameMatches := hmac.Equal(s.digestOf(username, ""), s.digestOf(state.Username, ""))
	passwordMatches := bcrypt.CompareHashAndPassword([]byte(state.PasswordHash), []byte(password)) == nil
	if !usernameMatches || !passwordMatches {
		return false
	}
	s.mu.Lock()
	s.last = remembered{passwordHash: state.PasswordHash, digest: s.digestOf(username, password)}
	s.mu.Unlock()
	return true
}

func (s *Service) hashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.hashCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func (s *Service) forget() {
	s.mu.Lock()
	s.last = remembered{}
	s.mu.Unlock()
}

// digestOf : HMAC sous la clé du processus ; la longueur fixe rend la
// comparaison indépendante de ce qui a été saisi.
func (s *Service) digestOf(username, password string) []byte {
	mac := hmac.New(sha256.New, s.rememberKey)
	mac.Write([]byte(username))
	mac.Write([]byte{0})
	mac.Write([]byte(password))
	return mac.Sum(nil)
}
