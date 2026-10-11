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
	if !service.Verify(state, "lucas", "correct horse battery") {
		t.Fatal("right credentials refused")
	}
	if !service.Remembered(state, "lucas", "correct horse battery") {
		t.Fatal("verified credentials must be remembered")
	}
	if service.Remembered(state, "lucas", "something else") || service.Verify(state, "lucas", "something else") {
		t.Fatal("a wrong password passed")
	}
	if service.Verify(state, "other", "correct horse battery") {
		t.Fatal("a wrong username passed")
	}
	changed, err := service.ChangePassword(ctx, "another long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if service.Remembered(changed, "lucas", "correct horse battery") || service.Verify(changed, "lucas", "correct horse battery") {
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
	if service.Verify(store.state, "", "") {
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
