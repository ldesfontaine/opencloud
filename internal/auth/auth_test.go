package auth

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// memoryStore tient l'état comme la base, sans elle.
type memoryStore struct {
	state State
}

func (m *memoryStore) GetAuthState(context.Context) (State, error) {
	if m.state.Mode == "" {
		return State{Mode: ModeNone}, nil
	}
	return m.state, nil
}

func (m *memoryStore) SaveAuthState(_ context.Context, state State, _ time.Time) error {
	m.state = state
	return nil
}

func newTestService(t *testing.T) (*Service, *memoryStore) {
	t.Helper()
	store := &memoryStore{}
	service, err := New(store, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	service.SetHashCost(bcrypt.MinCost)
	return service, store
}

// verifies rend le verdict d'une vérification qui ne doit pas échouer
// autrement que par un mauvais identifiant.
func verifies(t *testing.T, service *Service, state State, username, password string) bool {
	t.Helper()
	verified, err := service.Verify(context.Background(), state, username, password)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return verified
}

func TestEnableBasic_StoresASlowHashNeverThePassword(t *testing.T) {
	service, store := newTestService(t)
	if _, err := service.EnableBasic(context.Background(), "lucas", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if store.state.Mode != ModeBasic || !strings.HasPrefix(store.state.PasswordHash, "$2") {
		t.Fatalf("stored %+v", store.state)
	}
	if strings.Contains(store.state.PasswordHash, "correct horse") {
		t.Fatal("the password must not be stored")
	}
}

func TestValidate_RefusesShortPasswordsAndSeparatorInUsername(t *testing.T) {
	if !errors.Is(ValidatePassword("onze carac."), ErrPasswordTooShort) {
		t.Error("11 characters must be refused")
	}
	if ValidatePassword("douze carac.") != nil {
		t.Error("12 characters must pass")
	}
	if !errors.Is(ValidatePassword(strings.Repeat("a", 73)), ErrPasswordTooLong) {
		t.Error("bcrypt ignores beyond 72 bytes: refuse")
	}
	for _, name := range []string{"", "a:b", " lucas", "lu\ncas", strings.Repeat("a", 65)} {
		if !errors.Is(ValidateUsername(name), ErrUsernameInvalid) {
			t.Errorf("%q must be refused", name)
		}
	}
}

// Les identifiants vérifiés sont retenus, et oubliés dès que l'empreinte
// change : changement de mot de passe ou désactivation.
func TestVerify_RemembersUntilThePasswordChanges(t *testing.T) {
	service, store := newTestService(t)
	ctx := context.Background()
	state, err := service.EnableBasic(ctx, "lucas", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if service.Remembered(state, "lucas", "correct horse battery") {
		t.Fatal("nothing is remembered before a first verification")
	}
	if verified, err := service.Verify(ctx, state, "lucas", "correct horse battery"); err != nil || !verified {
		t.Fatalf("right credentials refused: %v, %v", verified, err)
	}
	if !service.Remembered(state, "lucas", "correct horse battery") {
		t.Fatal("verified credentials must be remembered")
	}
	if service.Remembered(state, "lucas", "something else") || verifies(t, service, state, "lucas", "something else") {
		t.Fatal("a wrong password passed")
	}
	if verifies(t, service, state, "other", "correct horse battery") {
		t.Fatal("a wrong username passed")
	}
	changed, err := service.ChangePassword(ctx, "another long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if service.Remembered(changed, "lucas", "correct horse battery") || verifies(t, service, changed, "lucas", "correct horse battery") {
		t.Fatal("the old password survived the change")
	}
	if err := service.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if store.state.Mode != ModeNone || store.state.PasswordHash != "" || store.state.Username != "" {
		t.Fatalf("disable must erase the credentials: %+v", store.state)
	}
}

func TestEnableForward_StoresTheHeaderAndNoCredentials(t *testing.T) {
	service, store := newTestService(t)
	ctx := context.Background()
	state, err := service.EnableForward(ctx, DefaultForwardHeader)
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != ModeForward || state.Header != "Remote-User" || state.EnabledAt.IsZero() {
		t.Fatalf("returned %+v", state)
	}
	if store.state.Mode != ModeForward || store.state.Header != "Remote-User" || store.state.Username != "" || store.state.PasswordHash != "" {
		t.Fatalf("stored %+v", store.state)
	}
	if _, err := service.EnableForward(ctx, "X-Forwarded-User"); !errors.Is(err, ErrAlreadyEnabled) {
		t.Fatalf("enable twice: %v", err)
	}
	if _, err := service.EnableBasic(ctx, "lucas", "correct horse battery"); !errors.Is(err, ErrAlreadyEnabled) {
		t.Fatalf("basic over forward: %v", err)
	}
	if _, err := service.ChangePassword(ctx, "another long passphrase"); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("change password in forward mode: %v", err)
	}
	if verifies(t, service, store.state, "", "") {
		t.Fatal("no credentials verify in forward mode")
	}
	if err := service.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if store.state.Mode != ModeNone || store.state.Header != "" {
		t.Fatalf("disable must erase the header: %+v", store.state)
	}
}

func TestEnableForward_RefusesAnInvalidHeaderName(t *testing.T) {
	service, store := newTestService(t)
	for _, header := range []string{"", "Remote User", "Remote-User:", "Remote_User", "Rémote-User", strings.Repeat("a", 65)} {
		if _, err := service.EnableForward(context.Background(), header); !errors.Is(err, ErrHeaderInvalid) {
			t.Errorf("%q: %v, want ErrHeaderInvalid", header, err)
		}
	}
	if store.state.Mode != "" {
		t.Fatalf("a refused header changed the state: %+v", store.state)
	}
	for _, header := range []string{"Remote-User", "X-Forwarded-User", "X-Auth-Request-Email", strings.Repeat("a", 64)} {
		if err := ValidateHeader(header); err != nil {
			t.Errorf("%q must pass: %v", header, err)
		}
	}
}

func TestModes_ListsEveryModeTheStoreAccepts(t *testing.T) {
	want := []Mode{ModeNone, ModeBasic, ModeForward}
	if got := Modes(); !slices.Equal(got, want) {
		t.Fatalf("modes %v, want %v", got, want)
	}
}

// holdVerification occupe la place de la vérification lente, comme le ferait
// un bcrypt en cours, et la rend à la fin du test.
func holdVerification(t *testing.T, service *Service) (release func()) {
	t.Helper()
	service.verifying <- struct{}{}
	released := false
	release = func() {
		if !released {
			released = true
			<-service.verifying
		}
	}
	t.Cleanup(release)
	return release
}

func waitForQueued(t *testing.T, service *Service, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for service.queued.Load() != want {
		if time.Now().After(deadline) {
			t.Fatalf("queued %d, want %d", service.queued.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

type verdict struct {
	verified bool
	err      error
}

func verifyInBackground(ctx context.Context, service *Service, state State, username, password string) <-chan verdict {
	result := make(chan verdict, 1)
	go func() {
		verified, err := service.Verify(ctx, state, username, password)
		result <- verdict{verified, err}
	}()
	return result
}

func newBasicState(t *testing.T, service *Service) State {
	t.Helper()
	state, err := service.EnableBasic(context.Background(), "lucas", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// Une requête qui prend fin pendant l'attente rend sa place dans la file et
// ne coûte aucun bcrypt : des identifiants justes ne passent pas non plus.
func TestVerify_CanceledWhileWaiting_ReturnsWithoutComparing(t *testing.T) {
	service, _ := newTestService(t)
	state := newBasicState(t, service)
	holdVerification(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	result := verifyInBackground(ctx, service, state, "lucas", "correct horse battery")
	waitForQueued(t, service, 1)
	cancel()
	got := <-result
	if got.verified || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("verdict %+v", got)
	}
	waitForQueued(t, service, 0)
	if service.Remembered(state, "lucas", "correct horse battery") {
		t.Fatal("a canceled verification must not have compared the password")
	}
}

func TestVerify_CanceledBeforeStarting_GivesTheSlotBack(t *testing.T) {
	service, _ := newTestService(t)
	state := newBasicState(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	verified, err := service.Verify(ctx, state, "lucas", "correct horse battery")
	if verified || !errors.Is(err, context.Canceled) {
		t.Fatalf("verified %v, err %v", verified, err)
	}
	if len(service.verifying) != 0 {
		t.Fatal("the slot was not given back")
	}
}

func TestVerify_FullQueue_RefusesAtOnce(t *testing.T) {
	service, _ := newTestService(t)
	state := newBasicState(t, service)
	holdVerification(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make([]<-chan verdict, 0, maxQueuedVerifications)
	for range maxQueuedVerifications {
		waiting = append(waiting, verifyInBackground(ctx, service, state, "lucas", "wrong password here"))
	}
	waitForQueued(t, service, maxQueuedVerifications)

	refused := verifyInBackground(context.Background(), service, state, "lucas", "correct horse battery")
	select {
	case got := <-refused:
		if got.verified || !errors.Is(got.err, ErrVerificationQueueFull) {
			t.Fatalf("verdict %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a full queue must refuse at once, not wait")
	}
	if got := service.queued.Load(); got != maxQueuedVerifications {
		t.Fatalf("a refusal changed the queue: %d", got)
	}

	cancel()
	for _, result := range waiting {
		if got := <-result; !errors.Is(got.err, context.Canceled) {
			t.Fatalf("waiting verdict %+v", got)
		}
	}
	waitForQueued(t, service, 0)
}

// Les identifiants déjà vérifiés ne font pas la queue : l'opérateur connecté
// n'est pas refusé parce qu'une file de mauvais mots de passe est pleine.
func TestVerify_RememberedCredentialsSkipTheQueue(t *testing.T) {
	service, _ := newTestService(t)
	state := newBasicState(t, service)
	if !verifies(t, service, state, "lucas", "correct horse battery") {
		t.Fatal("right credentials refused")
	}
	holdVerification(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for range maxQueuedVerifications {
		verifyInBackground(ctx, service, state, "lucas", "wrong password here")
	}
	waitForQueued(t, service, maxQueuedVerifications)
	verified, err := service.Verify(context.Background(), state, "lucas", "correct horse battery")
	if err != nil || !verified {
		t.Fatalf("remembered credentials: %v, %v", verified, err)
	}
}

// Les onglets de l'opérateur qui reviennent ensemble après un redémarrage
// (la mémoire est vide) remplissent la file sans être refusés : un seul
// bcrypt, les autres passent par la mémoire.
func TestVerify_OperatorTabsReturningTogether_AllPass(t *testing.T) {
	service, _ := newTestService(t)
	state := newBasicState(t, service)
	release := holdVerification(t, service)
	tabs := make([]<-chan verdict, 0, maxQueuedVerifications)
	for range maxQueuedVerifications {
		tabs = append(tabs, verifyInBackground(context.Background(), service, state, "lucas", "correct horse battery"))
	}
	waitForQueued(t, service, maxQueuedVerifications)
	release()
	for i, tab := range tabs {
		if got := <-tab; !got.verified || got.err != nil {
			t.Fatalf("tab %d: verdict %+v", i+1, got)
		}
	}
}

// L'opérateur qui arrive derrière quelques mauvaises tentatives attend son
// tour et passe, tant que la file n'est pas pleine.
func TestVerify_RightOperatorBehindAShortQueue_Passes(t *testing.T) {
	service, _ := newTestService(t)
	state := newBasicState(t, service)
	release := holdVerification(t, service)
	for range maxQueuedVerifications - 1 {
		verifyInBackground(context.Background(), service, state, "lucas", "wrong password here")
	}
	operator := verifyInBackground(context.Background(), service, state, "lucas", "correct horse battery")
	waitForQueued(t, service, maxQueuedVerifications)
	release()
	select {
	case got := <-operator:
		if !got.verified || got.err != nil {
			t.Fatalf("verdict %+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the operator never got through")
	}
}
