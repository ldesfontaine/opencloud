// Package auth tient la session et l'opérateur : mot de passe, changement forcé
// à la première connexion. Le modèle est prêt pour plusieurs comptes.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/validate"
)

const (
	// Identifiants de tout déploiement neuf ; le mot de passe doit changer
	// à la première connexion (08-securite-et-secrets.md).
	DefaultUsername = "admin"
	DefaultPassword = "opencloud"

	// Une session dure une journée puis demande de se reconnecter.
	sessionLifetime = 24 * time.Hour

	// Un mot de passe plus long que ça n'est pas tapé par un humain : on ne
	// le hache pas. L'OWASP demande d'accepter au moins 64 caractères.
	maxPasswordBytes = 256

	// Chaque vérification argon2id réserve 19 Mio : on n'en mène que
	// quelques-unes à la fois, les autres attendent leur tour.
	maxConcurrentPasswordChecks = 4
)

var (
	// ErrInvalidCredentials : identifiant ou mot de passe faux. Un seul message,
	// pour ne pas révéler lequel.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrTooManyAttempts : trop d'échecs récents sur cet identifiant.
	ErrTooManyAttempts = errors.New("too many login attempts")
	// ErrSessionExpired : jeton inconnu ou périmé.
	ErrSessionExpired = errors.New("session expired")
	// ErrPasswordEmpty, ErrPasswordIsDefault, ErrPasswordUnchanged : refus au
	// changement de mot de passe.
	ErrPasswordEmpty     = errors.New("password is empty")
	ErrPasswordIsDefault = errors.New("password is the default one")
	ErrPasswordUnchanged = errors.New("password is unchanged")
	ErrPasswordTooLong   = errors.New("password is too long")
)

// PasswordTooShortError : le nouveau mot de passe est sous la longueur
// minimale. Il porte la valeur pour que le refus la dise.
type PasswordTooShortError struct {
	MinLength int
}

func (e *PasswordTooShortError) Error() string {
	return fmt.Sprintf("password is shorter than %d characters", e.MinLength)
}

// PasswordPolicy est ce que la configuration impose aux nouveaux mots de
// passe. MinLength à 0 lève la règle (08-securite-et-secrets.md : aucune
// politique sans porte de sortie).
type PasswordPolicy struct {
	MinLength int
}

// Store est ce que auth attend de la base. Le vrai est *store.Store.
type Store interface {
	CountAccounts(ctx context.Context) (int, error)
	CreateAccount(ctx context.Context, account store.Account) (int64, error)
	FindAccountByUsername(ctx context.Context, username string) (store.Account, error)
	FindAccountByID(ctx context.Context, id int64) (store.Account, error)
	UpdateAccountPassword(ctx context.Context, id int64, passwordHash string, mustChange bool) error
	SetMustChangePassword(ctx context.Context, id int64, mustChange bool) error
	CreateSession(ctx context.Context, session store.Session) error
	FindSession(ctx context.Context, tokenHash string) (store.Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteSessionsOfAccount(ctx context.Context, accountID int64) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}

// Session est ce que reçoit l'appelant à la connexion : le jeton en clair,
// donné une seule fois, à mettre dans le cookie.
type Session struct {
	Token     string
	ExpiresAt time.Time
}

type Service struct {
	store     Store
	logger    *slog.Logger
	policy    PasswordPolicy
	throttle  *loginThrottle
	hashSlots chan struct{}
	now       func() time.Time
}

func New(store Store, logger *slog.Logger, policy PasswordPolicy) *Service {
	return &Service{
		store:     store,
		logger:    logger,
		policy:    policy,
		throttle:  newLoginThrottle(),
		hashSlots: make(chan struct{}, maxConcurrentPasswordChecks),
		now:       time.Now,
	}
}

// PasswordPolicy rend la règle en vigueur, pour que l'écran la dise.
func (s *Service) PasswordPolicy() PasswordPolicy {
	return s.policy
}

// EnsureDefaultAccount crée le compte par défaut si la base n'en a aucun, et
// tient l'obligation de changement à jour : due tant que le mot de passe est
// celui par défaut, sauf si la configuration l'autorise (développement).
// À appeler au démarrage, avant d'écouter.
func (s *Service) EnsureDefaultAccount(ctx context.Context, allowDefaultPassword bool) error {
	mustChange := !allowDefaultPassword
	if allowDefaultPassword {
		s.logger.Warn("default password allowed to stay: development only")
	}

	count, err := s.store.CountAccounts(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return s.createDefaultAccount(ctx, mustChange)
	}
	return s.refreshDefaultAccountObligation(ctx, mustChange)
}

func (s *Service) createDefaultAccount(ctx context.Context, mustChange bool) error {
	hash, err := HashPassword(DefaultPassword)
	if err != nil {
		return err
	}
	_, err = s.store.CreateAccount(ctx, store.Account{
		Username:           DefaultUsername,
		PasswordHash:       hash,
		MustChangePassword: mustChange,
	})
	if err != nil {
		return err
	}
	s.logger.Warn("default account created", "username", DefaultUsername, "must_change_password", mustChange)
	return nil
}

// Une base qui change de configuration (dev → prod, ou l'inverse) suit :
// l'obligation ne dépend que du mot de passe réel et de la configuration.
func (s *Service) refreshDefaultAccountObligation(ctx context.Context, mustChange bool) error {
	account, err := s.store.FindAccountByUsername(ctx, DefaultUsername)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	stillDefault, err := VerifyPassword(DefaultPassword, account.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify password of %s: %w", DefaultUsername, err)
	}
	if !stillDefault || account.MustChangePassword == mustChange {
		return nil
	}
	return s.store.SetMustChangePassword(ctx, account.ID, mustChange)
}

// Login vérifie les identifiants et ouvre une session. Ce qui ne peut pas
// être un identifiant ou un mot de passe est refusé avant de coûter quoi que
// ce soit — ni frein, ni requête, ni empreinte.
func (s *Service) Login(ctx context.Context, username, password string) (Session, error) {
	if err := validate.Username(username); err != nil {
		return Session{}, ErrInvalidCredentials
	}
	if len(password) > maxPasswordBytes {
		return Session{}, ErrInvalidCredentials
	}

	now := s.now()
	if !s.throttle.allow(username, now) {
		return Session{}, ErrTooManyAttempts
	}

	release, err := s.acquireHashSlot(ctx)
	if err != nil {
		return Session{}, err
	}
	matches, err := s.verifyLogin(ctx, username, password)
	release()
	if err != nil {
		return Session{}, err
	}
	if !matches {
		s.throttle.recordFailure(username, now)
		return Session{}, ErrInvalidCredentials
	}
	s.throttle.reset(username)

	account, err := s.store.FindAccountByUsername(ctx, username)
	if err != nil {
		return Session{}, err
	}
	// Le ménage des sessions périmées se fait ici : à chaque connexion, jamais
	// dans un fil à part.
	if err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
		return Session{}, err
	}
	return s.openSession(ctx, account.ID)
}

// verifyLogin rend vrai si le couple est bon. Un compte inconnu coûte le même
// temps qu'un mauvais mot de passe : on vérifie une empreinte quand même.
func (s *Service) verifyLogin(ctx context.Context, username, password string) (bool, error) {
	account, err := s.store.FindAccountByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = VerifyPassword(password, unknownAccountHash)
		return false, nil
	}
	if err != nil {
		return false, err
	}

	matches, err := VerifyPassword(password, account.PasswordHash)
	if err != nil {
		return false, fmt.Errorf("verify password of %s: %w", username, err)
	}
	return matches, nil
}

// Authenticate retrouve le compte d'un jeton de session, ou ErrSessionExpired.
func (s *Service) Authenticate(ctx context.Context, token string) (store.Account, error) {
	session, err := s.store.FindSession(ctx, hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return store.Account{}, ErrSessionExpired
	}
	if err != nil {
		return store.Account{}, err
	}
	if !s.now().Before(session.ExpiresAt) {
		// Ménage au passage ; s'il échoue, la session reste périmée quand même.
		_ = s.store.DeleteSession(ctx, session.TokenHash)
		return store.Account{}, ErrSessionExpired
	}

	account, err := s.store.FindAccountByID(ctx, session.AccountID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Account{}, ErrSessionExpired
	}
	return account, err
}

// Logout ferme la session ; un jeton inconnu n'est pas une erreur.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, hashToken(token))
}

// ChangePassword vérifie l'ancien mot de passe, pose le nouveau, ferme toutes
// les sessions du compte et en ouvre une neuve pour l'appelant.
func (s *Service) ChangePassword(ctx context.Context, accountID int64, currentPassword, newPassword string) (Session, error) {
	account, err := s.store.FindAccountByID(ctx, accountID)
	if err != nil {
		return Session{}, err
	}

	// Une seule place pour les deux empreintes : vérifier l'ancien, hacher le nouveau.
	release, err := s.acquireHashSlot(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()

	matches, err := VerifyPassword(currentPassword, account.PasswordHash)
	if err != nil {
		return Session{}, fmt.Errorf("verify password of %s: %w", account.Username, err)
	}
	if !matches {
		return Session{}, ErrInvalidCredentials
	}
	if err := s.checkNewPassword(newPassword, currentPassword); err != nil {
		return Session{}, err
	}

	hash, err := HashPassword(newPassword)
	if err != nil {
		return Session{}, err
	}
	if err := s.store.UpdateAccountPassword(ctx, accountID, hash, false); err != nil {
		return Session{}, err
	}
	if err := s.store.DeleteSessionsOfAccount(ctx, accountID); err != nil {
		return Session{}, err
	}
	s.logger.Info("password changed", "username", account.Username)
	return s.openSession(ctx, accountID)
}

// La seule règle imposée est la longueur minimale de la configuration
// (OWASP ASVS 2.1.1 : douze caractères pour un compte d'administration), en
// caractères, pas en octets. Le reste refuse ce qui n'est pas un changement.
func (s *Service) checkNewPassword(newPassword, currentPassword string) error {
	if newPassword == "" {
		return ErrPasswordEmpty
	}
	if newPassword == DefaultPassword {
		return ErrPasswordIsDefault
	}
	if newPassword == currentPassword {
		return ErrPasswordUnchanged
	}
	if utf8.RuneCountInString(newPassword) < s.policy.MinLength {
		return &PasswordTooShortError{MinLength: s.policy.MinLength}
	}
	if len(newPassword) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

// acquireHashSlot attend une place pour hacher ; release la rend. L'attente
// s'arrête avec le contexte de la requête.
func (s *Service) acquireHashSlot(ctx context.Context) (release func(), err error) {
	select {
	case s.hashSlots <- struct{}{}:
		return func() { <-s.hashSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) openSession(ctx context.Context, accountID int64) (Session, error) {
	token := rand.Text()
	now := s.now()
	session := store.Session{
		TokenHash: hashToken(token),
		AccountID: accountID,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionLifetime),
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return Session{}, err
	}
	return Session{Token: token, ExpiresAt: session.ExpiresAt}, nil
}

// La base ne garde que l'empreinte : lire la base ne donne pas les sessions.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Empreinte d'un mot de passe quelconque, vérifiée quand le compte n'existe
// pas, pour un temps de réponse identique.
const unknownAccountHash = "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
