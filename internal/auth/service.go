package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Coût bcrypt : environ 100 ms par vérification sur une machine modeste.
// La mémoire des identifiants vérifiés évite de le payer à chaque requête.
const (
	DefaultHashCost = 12
	rememberKeyLen  = 32
	// Les vérifications qui attendent derrière celle qui tourne. La file tient
	// la rafale entière qu'une adresse a le droit de présenter (dix essais,
	// voir authAttemptBurst dans internal/server) : les onglets de l'opérateur
	// qui reviennent ensemble après un redémarrage attendent le premier, puis
	// passent par la mémoire, au lieu d'être refusés. Au-delà, refus immédiat :
	// l'attente d'un opérateur admis reste bornée à dix bcrypt.
	maxQueuedVerifications = 9
)

// ErrVerificationQueueFull : trop de vérifications attendent déjà. L'appelant
// répond 429 sans rien comparer ; ce n'est pas un mauvais mot de passe.
var ErrVerificationQueueFull = errors.New("auth: verification queue full")

// Ce que le service attend du store.
type Store interface {
	GetAuthState(ctx context.Context) (State, error)
	// SaveAuthStateIf n'écrit que si l'état stocké vaut encore expected, et
	// dit s'il a écrit.
	SaveAuthStateIf(ctx context.Context, expected, next State, at time.Time) (bool, error)
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
	// Un canal d'une place plutôt qu'un mutex, pour qu'une attente puisse
	// être abandonnée avec la requête qui la porte.
	verifying chan struct{}
	// Le nombre d'attentes en cours, pour refuser au-delà de la borne.
	queued atomic.Int32
}

func New(store Store, logger *slog.Logger) (*Service, error) {
	key := make([]byte, rememberKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate remember key: %w", err)
	}
	return &Service{
		store: store, logger: logger, now: time.Now, hashCost: DefaultHashCost,
		rememberKey: key, verifying: make(chan struct{}, 1),
	}, nil
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

// Les quatre changements d'état prennent l'état sous lequel la requête a été
// autorisée (expected) : une requête dont le corps a traîné ne s'applique pas
// à un état qu'elle n'a pas vu. La comparaison et l'écriture se font d'un seul
// tenant dans la base ; la lecture qui précède évite seulement un hachage
// inutile.

// EnableBasic choisit identifiant et mot de passe, et passe en mode basic.
func (s *Service) EnableBasic(ctx context.Context, expected State, username, password string) (State, error) {
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
	if err := s.replaceState(ctx, expected, next); err != nil {
		return State{}, err
	}
	s.logger.Info("interface authentication enabled", "mode", string(ModeBasic), "username", username)
	return next, nil
}

// EnableForward passe en mode forward : l'identité vient de l'en-tête
// que pose le mandataire. Le serveur vérifie d'abord que la requête qui
// active le porte déjà, sans quoi l'opérateur s'enfermerait dehors.
func (s *Service) EnableForward(ctx context.Context, expected State, header string) (State, error) {
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
	if err := s.replaceState(ctx, expected, next); err != nil {
		return State{}, err
	}
	s.logger.Info("interface authentication enabled", "mode", string(ModeForward), "header", header)
	return next, nil
}

// ChangePassword garde l'identifiant et remplace l'empreinte.
func (s *Service) ChangePassword(ctx context.Context, expected State, password string) (State, error) {
	current, err := s.store.GetAuthState(ctx)
	if err != nil {
		return State{}, err
	}
	if !current.Same(expected) {
		return State{}, ErrStateChanged
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
	next := current
	next.PasswordHash = hash
	if err := s.replaceState(ctx, expected, next); err != nil {
		return State{}, err
	}
	s.logger.Info("interface password changed", "username", next.Username)
	return next, nil
}

// Disable rouvre l'interface et efface identifiant, empreinte et en-tête.
func (s *Service) Disable(ctx context.Context, expected State) error {
	current, err := s.store.GetAuthState(ctx)
	if err != nil {
		return err
	}
	if !current.Same(expected) {
		return ErrStateChanged
	}
	if current.Mode == ModeNone {
		return ErrDisabled
	}
	if err := s.replaceState(ctx, expected, State{Mode: ModeNone}); err != nil {
		return err
	}
	s.logger.Warn("interface authentication disabled", "previous_mode", string(current.Mode))
	return nil
}

// replaceState écrit next si l'état stocké vaut encore expected, et oublie
// les identifiants retenus.
func (s *Service) replaceState(ctx context.Context, expected, next State) error {
	swapped, err := s.store.SaveAuthStateIf(ctx, expected, next, s.now())
	if err != nil {
		return err
	}
	if !swapped {
		return ErrStateChanged
	}
	s.forget()
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
//
// L'erreur dit qu'on n'a rien comparé : la file est pleine
// (ErrVerificationQueueFull), ou la requête a pris fin pendant l'attente.
// Un bcrypt déjà commencé ne s'interrompt pas, mais aucun ne commence pour
// une requête abandonnée.
func (s *Service) Verify(ctx context.Context, state State, username, password string) (bool, error) {
	if state.Mode != ModeBasic || state.PasswordHash == "" {
		return false, nil
	}
	// Des identifiants déjà vérifiés ne font jamais la queue.
	if s.Remembered(state, username, password) {
		return true, nil
	}
	if err := s.acquireVerification(ctx); err != nil {
		return false, err
	}
	defer func() { <-s.verifying }()
	// La vérification qu'on vient d'attendre a pu retenir ces identifiants.
	if s.Remembered(state, username, password) {
		return true, nil
	}
	usernameMatches := hmac.Equal(s.digestOf(username, ""), s.digestOf(state.Username, ""))
	passwordMatches := bcrypt.CompareHashAndPassword([]byte(state.PasswordHash), []byte(password)) == nil
	if !usernameMatches || !passwordMatches {
		return false, nil
	}
	s.mu.Lock()
	s.last = remembered{passwordHash: state.PasswordHash, digest: s.digestOf(username, password)}
	s.mu.Unlock()
	return true, nil
}

// acquireVerification prend la place de la vérification lente. Si elle est
// prise, on entre dans la file, bornée : au-delà, refus immédiat.
func (s *Service) acquireVerification(ctx context.Context) error {
	select {
	case s.verifying <- struct{}{}:
		return s.stopIfCanceled(ctx)
	default:
	}
	if s.queued.Add(1) > maxQueuedVerifications {
		s.queued.Add(-1)
		return ErrVerificationQueueFull
	}
	defer s.queued.Add(-1)
	select {
	case s.verifying <- struct{}{}:
		return s.stopIfCanceled(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopIfCanceled rend la place si la requête a pris fin au moment où elle
// l'obtenait : select choisit au hasard quand les deux sont prêts.
func (s *Service) stopIfCanceled(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		<-s.verifying
		return err
	}
	return nil
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
