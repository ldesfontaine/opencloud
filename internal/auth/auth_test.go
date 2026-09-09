package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/migrations"
)

const testMinPasswordLength = 12

// Les tests qui ne portent pas sur le frein par adresse n'en donnent pas :
// une adresse invalide traverse ce frein sans le nourrir.
var noAddress netip.Addr

func newTestService(t *testing.T) *Service {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })

	logger := slog.New(slog.DiscardHandler)
	testStore, err := store.Open(context.Background(), root, migrations.Files, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testStore.Close() })

	service := New(testStore, logger, PasswordPolicy{MinLength: testMinPasswordLength})
	if err := service.EnsureDefaultAccount(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestHashPassword_VerifyPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}

	matches, err := VerifyPassword("s3cret", hash)
	if err != nil || !matches {
		t.Fatalf("le bon mot de passe doit passer : matches=%v err=%v", matches, err)
	}
	matches, err = VerifyPassword("wrong", hash)
	if err != nil || matches {
		t.Fatalf("un mauvais mot de passe doit échouer : matches=%v err=%v", matches, err)
	}
}

func TestVerifyPassword_MalformedHash_IsAnError(t *testing.T) {
	_, err := VerifyPassword("x", "$bcrypt$nope")
	if !errors.Is(err, ErrMalformedHash) {
		t.Fatalf("attendu ErrMalformedHash, reçu %v", err)
	}
}

func TestEnsureDefaultAccount_CreatesOnce(t *testing.T) {
	service := newTestService(t)

	if err := service.EnsureDefaultAccount(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	count, _ := service.store.CountAccounts(context.Background())
	if count != 1 {
		t.Fatalf("comptes = %d, attendu 1", count)
	}
}

func TestEnsureDefaultAccount_AllowDefault_LiftsAndRestoresObligation(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)

	// Développement : l'obligation tombe, même sur une base déjà créée en production.
	if err := service.EnsureDefaultAccount(ctx, true); err != nil {
		t.Fatal(err)
	}
	account, _ := service.store.FindAccountByUsername(ctx, DefaultUsername)
	if account.MustChangePassword {
		t.Fatal("en développement, le mot de passe par défaut peut rester")
	}

	// Retour en production : le mot de passe est toujours celui par défaut, l'obligation revient.
	if err := service.EnsureDefaultAccount(ctx, false); err != nil {
		t.Fatal(err)
	}
	account, _ = service.store.FindAccountByUsername(ctx, DefaultUsername)
	if !account.MustChangePassword {
		t.Fatal("en production, un mot de passe encore par défaut doit changer")
	}
}

func TestEnsureDefaultAccount_ChangedPassword_IsLeftAlone(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	session, _ := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress)
	account, _ := service.Authenticate(ctx, session.Token)
	if _, err := service.ChangePassword(ctx, account.ID, DefaultPassword, "settled-for-good"); err != nil {
		t.Fatal(err)
	}

	if err := service.EnsureDefaultAccount(ctx, false); err != nil {
		t.Fatal(err)
	}

	account, _ = service.store.FindAccountByID(ctx, account.ID)
	if account.MustChangePassword {
		t.Fatal("un mot de passe déjà changé n'a rien à changer")
	}
}

func TestLogin_TooManyFailures_IsThrottledUntilWindowPasses(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)

	for attempt := 0; attempt < maxLoginFailures; attempt++ {
		if _, err := service.Login(ctx, DefaultUsername, "wrong", noAddress); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("tentative %d : attendu ErrInvalidCredentials, reçu %v", attempt, err)
		}
	}

	if _, err := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("même le bon mot de passe doit attendre : reçu %v", err)
	}
	if _, err := service.Login(ctx, "someone-else", DefaultPassword, noAddress); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("un autre identifiant n'est pas bloqué : reçu %v", err)
	}

	service.now = func() time.Time { return time.Now().Add(loginFailureWindow + time.Second) }
	if _, err := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress); err != nil {
		t.Fatalf("la fenêtre passée, la connexion doit marcher : %v", err)
	}
}

func TestLogin_TooManyFailuresFromOneAddress_IsRefusedWithADelay(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	address := netip.MustParseAddr("203.0.113.7")

	// Un identifiant différent à chaque fois : seul le frein par adresse
	// peut arrêter celui-là.
	for attempt := 0; attempt < maxFailuresPerAddress; attempt++ {
		username := fmt.Sprintf("guess-%d", attempt)
		if _, err := service.Login(ctx, username, "wrong", address); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("tentative %d : attendu ErrInvalidCredentials, reçu %v", attempt, err)
		}
	}

	_, err := service.Login(ctx, DefaultUsername, DefaultPassword, address)
	var throttled *TooManyAttemptsFromAddressError
	if !errors.As(err, &throttled) {
		t.Fatalf("la 21e tentative doit être refusée par l'adresse, reçu %v", err)
	}
	if throttled.RetryIn <= 0 || throttled.RetryIn > loginFailureWindow {
		t.Fatalf("délai = %s, attendu entre 0 et %s", throttled.RetryIn, loginFailureWindow)
	}
	if !errors.Is(err, ErrTooManyAttempts) {
		t.Fatal("le refus par adresse reste un refus de frein")
	}
	if _, err := service.Login(ctx, DefaultUsername, DefaultPassword, netip.MustParseAddr("198.51.100.2")); err != nil {
		t.Fatalf("une autre adresse n'est pas freinée : %v", err)
	}
}

func TestLogin_SuccessFromAnAddress_ClearsItsFailures(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	address := netip.MustParseAddr("203.0.113.7")

	for attempt := 0; attempt < maxFailuresPerAddress-1; attempt++ {
		if _, err := service.Login(ctx, fmt.Sprintf("guess-%d", attempt), "wrong", address); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("tentative %d : attendu ErrInvalidCredentials, reçu %v", attempt, err)
		}
	}

	if _, err := service.Login(ctx, DefaultUsername, DefaultPassword, address); err != nil {
		t.Fatalf("la connexion doit marcher : %v", err)
	}

	if failures := len(service.addressThrottle.failures); failures != 0 {
		t.Fatalf("après une réussite, l'adresse ne compte plus d'échec, reçu %d", failures)
	}
}

func TestLogin_DefaultCredentials_OpensSessionThatMustChangePassword(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)

	session, err := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if session.Token == "" {
		t.Fatal("le jeton doit être rendu")
	}

	account, err := service.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !account.MustChangePassword {
		t.Fatal("le mot de passe par défaut doit être changé")
	}
}

func TestLogin_WrongPasswordOrUnknownUser_IsInvalidCredentials(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)

	for _, attempt := range [][2]string{{DefaultUsername, "wrong"}, {"nobody", DefaultPassword}} {
		_, err := service.Login(ctx, attempt[0], attempt[1], noAddress)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("%v : attendu ErrInvalidCredentials, reçu %v", attempt, err)
		}
	}
}

func TestAuthenticate_UnknownOrExpiredToken_IsSessionExpired(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)

	if _, err := service.Authenticate(ctx, "never-issued"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("attendu ErrSessionExpired, reçu %v", err)
	}

	session, err := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Now().Add(sessionLifetime + time.Minute) }
	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("attendu ErrSessionExpired après le délai, reçu %v", err)
	}
}

func TestLogout_ClosesSession(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	session, _ := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress)

	if err := service.Logout(ctx, session.Token); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("attendu ErrSessionExpired, reçu %v", err)
	}
}

func TestChangePassword_LiftsObligationAndClosesOtherSessions(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	first, _ := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress)
	account, _ := service.Authenticate(ctx, first.Token)

	fresh, err := service.ChangePassword(ctx, account.ID, DefaultPassword, "new-and-long")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if _, err := service.Authenticate(ctx, first.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("l'ancienne session doit être fermée, reçu %v", err)
	}
	updated, err := service.Authenticate(ctx, fresh.Token)
	if err != nil {
		t.Fatalf("la nouvelle session doit marcher : %v", err)
	}
	if updated.MustChangePassword {
		t.Fatal("l'obligation doit être levée")
	}
	if _, err := service.Login(ctx, DefaultUsername, "new-and-long", noAddress); err != nil {
		t.Fatalf("le nouveau mot de passe doit ouvrir une session : %v", err)
	}
}

func TestChangePassword_SameAsCurrent_IsRefused(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	session, _ := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress)
	account, _ := service.Authenticate(ctx, session.Token)
	if _, err := service.ChangePassword(ctx, account.ID, DefaultPassword, "settled-for-good"); err != nil {
		t.Fatal(err)
	}

	_, err := service.ChangePassword(ctx, account.ID, "settled-for-good", "settled-for-good")

	if !errors.Is(err, ErrPasswordUnchanged) {
		t.Fatalf("attendu ErrPasswordUnchanged, reçu %v", err)
	}
}

func TestChangePassword_Refusals(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	session, _ := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress)
	account, _ := service.Authenticate(ctx, session.Token)

	cases := []struct {
		name    string
		current string
		next    string
		want    error
	}{
		{"mauvais mot de passe actuel", "wrong", "whatever-long-enough", ErrInvalidCredentials},
		{"nouveau vide", DefaultPassword, "", ErrPasswordEmpty},
		{"nouveau trop long", DefaultPassword, strings.Repeat("x", maxPasswordBytes+1), ErrPasswordTooLong},
		{"nouveau = défaut", DefaultPassword, DefaultPassword, ErrPasswordIsDefault},
	}
	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			_, err := service.ChangePassword(ctx, account.ID, current.current, current.next)
			if !errors.Is(err, current.want) {
				t.Fatalf("attendu %v, reçu %v", current.want, err)
			}
		})
	}
}

func TestChangePassword_TooShort_IsRefusedWithTheMinimum(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	session, _ := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress)
	account, _ := service.Authenticate(ctx, session.Token)

	_, err := service.ChangePassword(ctx, account.ID, DefaultPassword, "onze-carac.")

	var tooShort *PasswordTooShortError
	if !errors.As(err, &tooShort) || tooShort.MinLength != testMinPasswordLength {
		t.Fatalf("attendu PasswordTooShortError{%d}, reçu %v", testMinPasswordLength, err)
	}
	// Douze caractères, pas douze octets : les accents comptent pour un.
	if _, err := service.ChangePassword(ctx, account.ID, DefaultPassword, "éèàùçôîâêû-1"); err != nil {
		t.Fatalf("douze caractères accentués doivent passer : %v", err)
	}
}

func TestChangePassword_NoMinimum_AcceptsShortPassword(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	service.policy = PasswordPolicy{MinLength: 0}
	session, _ := service.Login(ctx, DefaultUsername, DefaultPassword, noAddress)
	account, _ := service.Authenticate(ctx, session.Token)

	if _, err := service.ChangePassword(ctx, account.ID, DefaultPassword, "abc"); err != nil {
		t.Fatalf("sans minimum, un mot de passe court passe : %v", err)
	}
}

func TestLogin_MalformedOrHugeInput_IsRefusedBeforeAnyWork(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)

	attempts := [][2]string{
		{"Admin Root", DefaultPassword},
		{strings.Repeat("a", 65), DefaultPassword},
		{DefaultUsername, strings.Repeat("p", maxPasswordBytes+1)},
	}
	for _, attempt := range attempts {
		if _, err := service.Login(ctx, attempt[0], attempt[1], noAddress); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("%q : attendu ErrInvalidCredentials, reçu %v", attempt[0], err)
		}
	}
	if len(service.throttle.failures) != 0 || len(service.throttle.attempts) != 0 {
		t.Fatal("une entrée hors forme ne doit pas être comptée par le frein")
	}
}
