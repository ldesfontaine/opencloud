package machine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"
)

// Un faux magasin en mémoire : le vrai est testé chez lui, et le service
// n'a besoin que du contrat.
type fakeStore struct {
	machines map[string]Machine
	tokens   map[string]Token
}

func newFakeStore() *fakeStore {
	return &fakeStore{machines: map[string]Machine{}, tokens: map[string]Token{}}
}

func (f *fakeStore) SaveLocalMachine(_ context.Context, m Machine) error {
	f.machines[m.ID] = m
	return nil
}

func (f *fakeStore) ListMachines(context.Context) ([]Machine, error) {
	var out []Machine
	for _, m := range f.machines {
		out = append(out, m)
	}
	return out, nil
}

func (f *fakeStore) GetMachine(_ context.Context, id string) (Machine, error) {
	m, ok := f.machines[id]
	if !ok {
		return Machine{}, ErrNotFound
	}
	return m, nil
}

func (f *fakeStore) DeleteMachine(_ context.Context, id string) error {
	delete(f.machines, id)
	return nil
}

func (f *fakeStore) TouchMachine(_ context.Context, id string, at time.Time) error {
	m := f.machines[id]
	m.LastSeenAt = at
	f.machines[id] = m
	return nil
}

func (f *fakeStore) RecordConnection(_ context.Context, id, address, version string, at time.Time) error {
	m := f.machines[id]
	m.Address, m.AgentVersion, m.LastSeenAt = address, version, at
	f.machines[id] = m
	return nil
}

func (f *fakeStore) InsertToken(_ context.Context, t Token) error {
	f.tokens[t.Hash] = t
	return nil
}

func (f *fakeStore) ListPendingTokens(context.Context, time.Time) ([]Token, error) {
	var out []Token
	for _, t := range f.tokens {
		if t.ConsumedAt.IsZero() {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteToken(_ context.Context, id string) error {
	for hash, t := range f.tokens {
		if t.ID == id {
			delete(f.tokens, hash)
			return nil
		}
	}
	return ErrTokenNotFound
}

func (f *fakeStore) Enroll(_ context.Context, hash string, m Machine, now time.Time) (Machine, error) {
	t, ok := f.tokens[hash]
	if !ok {
		return Machine{}, ErrTokenNotFound
	}
	if !t.ConsumedAt.IsZero() {
		return Machine{}, ErrTokenConsumed
	}
	t.ConsumedAt = now
	f.tokens[hash] = t
	m.Name = t.Name
	if t.MachineID != "" {
		m.ID = t.MachineID
	}
	f.machines[m.ID] = m
	return m, nil
}

type harness struct {
	service *Service
	store   *fakeStore
	now     time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{store: newFakeStore(), now: time.Unix(1_800_000_000, 0)}
	h.service = New(h.store, NewSessions(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	h.service.SetClock(func() time.Time { return h.now })
	return h
}

func (h *harness) enroll(t *testing.T, name string) (Machine, ed25519.PrivateKey) {
	t.Helper()
	cleartext, _, err := h.service.CreateToken(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	m, err := h.service.Enroll(context.Background(), Enrollment{MachineID: id, PublicKey: public, Token: cleartext})
	if err != nil {
		t.Fatal(err)
	}
	return m, private
}

func (h *harness) prove(t *testing.T, m Machine, private ed25519.PrivateKey, at time.Time) Proof {
	t.Helper()
	nonce, err := h.service.Challenge(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := SignedPayload(nonce, m.ID, at.Unix())
	if err != nil {
		t.Fatal(err)
	}
	return Proof{MachineID: m.ID, Nonce: nonce, Timestamp: at.Unix(), Signature: Sign(private, payload)}
}

func TestCreateToken_RefusesABadName(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"", "VPS", "vps paris", "-vps", "vps-", "a.b"} {
		if _, _, err := h.service.CreateToken(context.Background(), name); !errors.Is(err, ErrNameInvalid) {
			t.Errorf("%q accepted: %v", name, err)
		}
	}
	cleartext, token, err := h.service.CreateToken(context.Background(), "vps-paris-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cleartext) < 40 || token.Prefix != cleartext[:9] || token.Hash != HashToken(cleartext) {
		t.Fatalf("token %q %+v", cleartext, token)
	}
}

func TestEnroll_RefusesBadIDAndKey(t *testing.T) {
	h := newHarness(t)
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := h.service.Enroll(context.Background(), Enrollment{MachineID: "not-a-uuid", PublicKey: public}); !errors.Is(err, ErrBadID) {
		t.Errorf("bad id: %v", err)
	}
	id, _ := NewID()
	if _, err := h.service.Enroll(context.Background(), Enrollment{MachineID: id, PublicKey: []byte("short")}); !errors.Is(err, ErrBadKey) {
		t.Errorf("bad key: %v", err)
	}
}

func TestAuthenticate_AcceptsAFreshSignedChallengeOnce(t *testing.T) {
	h := newHarness(t)
	m, private := h.enroll(t, "vps-paris-1")
	proof := h.prove(t, m, private, h.now)
	if _, err := h.service.Authenticate(context.Background(), proof); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Authenticate(context.Background(), proof); !errors.Is(err, ErrBadNonce) {
		t.Fatalf("replayed proof: %v", err)
	}
}

func TestAuthenticate_RefusesSkewWrongKeyAndForeignNonce(t *testing.T) {
	h := newHarness(t)
	m, private := h.enroll(t, "vps-paris-1")

	late := h.prove(t, m, private, h.now.Add(-10*time.Minute))
	if _, err := h.service.Authenticate(context.Background(), late); !errors.Is(err, ErrClockSkew) {
		t.Errorf("skew: %v", err)
	}
	_, wrong, _ := ed25519.GenerateKey(rand.Reader)
	forged := h.prove(t, m, wrong, h.now)
	if _, err := h.service.Authenticate(context.Background(), forged); !errors.Is(err, ErrBadSignature) {
		t.Errorf("wrong key: %v", err)
	}
	other, _ := h.enroll(t, "vps-lyon-2")
	stolen := h.prove(t, other, private, h.now)
	stolen.MachineID = m.ID
	if _, err := h.service.Authenticate(context.Background(), stolen); !errors.Is(err, ErrBadNonce) {
		t.Errorf("foreign nonce: %v", err)
	}
	h.now = h.now.Add(3 * time.Minute)
	stale := Proof{MachineID: m.ID, Nonce: make([]byte, NonceSize), Timestamp: h.now.Unix()}
	if _, err := h.service.Authenticate(context.Background(), stale); !errors.Is(err, ErrBadNonce) {
		t.Errorf("unknown nonce: %v", err)
	}
}

func TestLocalMachine_IsAlwaysOnlineAndNeverRemovedNorAuthenticated(t *testing.T) {
	h := newHarness(t)
	if err := h.service.EnsureLocal(context.Background(), LocalInfo{Hostname: "oc", OS: "Debian 12", Version: "v0.0.1"}); err != nil {
		t.Fatal(err)
	}
	status, err := h.service.Get(context.Background(), LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Online || !status.LastSeenAt.Equal(h.now) || status.Name != "opencloud" {
		t.Fatalf("got %+v", status)
	}
	if err := h.service.Remove(context.Background(), LocalID); !errors.Is(err, ErrLocalMachine) {
		t.Errorf("remove: %v", err)
	}
	if _, _, err := h.service.CreateReenrollToken(context.Background(), LocalID); !errors.Is(err, ErrLocalMachine) {
		t.Errorf("reenroll: %v", err)
	}
	if _, err := h.service.Challenge(context.Background(), LocalID); !errors.Is(err, ErrNotFound) {
		t.Errorf("challenge: %v", err)
	}
}

func TestConnectSignalDisconnect_DriveOnlineAndLastSeen(t *testing.T) {
	h := newHarness(t)
	m, _ := h.enroll(t, "vps-paris-1")
	if status, _ := h.service.Get(context.Background(), m.ID); status.Online {
		t.Fatal("online before any connection")
	}
	session, err := h.service.Connect(context.Background(), m.ID, "51.15.20.114", "v0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	status, _ := h.service.Get(context.Background(), m.ID)
	if !status.Online || status.Address != "51.15.20.114" {
		t.Fatalf("after connect: %+v", status)
	}
	h.now = h.now.Add(30 * time.Second)
	if err := h.service.Signal(context.Background(), session.Token); err != nil {
		t.Fatal(err)
	}
	status, _ = h.service.Get(context.Background(), m.ID)
	if !status.LastSeenAt.Equal(h.now) {
		t.Fatalf("signal did not touch last seen: %+v", status)
	}
	if err := h.service.Signal(context.Background(), "bogus"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("bogus session: %v", err)
	}
	h.service.Disconnect(session)
	if status, _ := h.service.Get(context.Background(), m.ID); status.Online {
		t.Fatal("still online after disconnect")
	}
	if err := h.service.Signal(context.Background(), session.Token); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("signal after disconnect: %v", err)
	}
}

func TestRemove_ClosesTheOpenSession(t *testing.T) {
	h := newHarness(t)
	m, _ := h.enroll(t, "vps-paris-1")
	session, err := h.service.Connect(context.Background(), m.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.service.Remove(context.Background(), m.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("session not closed")
	}
}

func TestSessions_NewConnectionReplacesThePreviousOne(t *testing.T) {
	sessions := NewSessions()
	first, _ := sessions.Open("m", time.Now())
	second, _ := sessions.Open("m", time.Now())
	select {
	case <-first.Done():
	default:
		t.Fatal("first session still open")
	}
	sessions.Release(first)
	if !sessions.IsOnline("m") {
		t.Fatal("releasing the old session must not close the new one")
	}
	sessions.Release(second)
	if sessions.IsOnline("m") || sessions.CountOnline() != 0 {
		t.Fatal("still online")
	}
}
