package machine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
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

func (f *fakeStore) MachineDeletionPlan(context.Context, string) (deletion.Plan, error) {
	return deletion.Plan{}, nil
}

func (f *fakeStore) TouchAgent(_ context.Context, agentID string, at time.Time) error {
	for id, m := range f.machines {
		if m.Agent.ID == agentID {
			m.Agent.LastSeenAt, m.LastSeenAt = at, at
			f.machines[id] = m
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeStore) RecordSystem(_ context.Context, id, system string) error {
	m := f.machines[id]
	m.OS = system
	f.machines[id] = m
	return nil
}

func (f *fakeStore) RecordRootDir(_ context.Context, agentID, root string) error {
	for id, m := range f.machines {
		if m.Agent.ID == agentID {
			m.Agent.RootDir = root
			f.machines[id] = m
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeStore) RecordServerKey(_ context.Context, agentID, fingerprint string) error {
	for id, m := range f.machines {
		if m.Agent.ID == agentID {
			m.Agent.ServerKeyFingerprint = fingerprint
			f.machines[id] = m
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeStore) RecordConnection(_ context.Context, c Connection) error {
	m := f.machines[c.MachineID]
	m.Address, m.Agent.Version, m.Agent.LastSeenAt, m.LastSeenAt = c.Address, c.Version, c.At, c.At
	if c.Capabilities != nil {
		m.Agent.Capabilities, m.Agent.ReportedAt = c.Capabilities, c.At
	}
	f.machines[c.MachineID] = m
	return nil
}

func (f *fakeStore) RevokeAgent(_ context.Context, machineID string, now time.Time) (string, error) {
	m := f.machines[machineID]
	if !m.Agent.IsActive() {
		return "", ErrNoAgent
	}
	m.Agent.RevokedAt = now
	f.machines[machineID] = m
	for hash, t := range f.tokens {
		if t.MachineID == machineID && t.ConsumedAt.IsZero() {
			delete(f.tokens, hash)
		}
	}
	return m.Agent.ID, nil
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

func (f *fakeStore) Enroll(_ context.Context, hash string, m Machine, agent Agent, now time.Time) (Machine, error) {
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
	agent.MachineID = m.ID
	agent.Requested = t.Capabilities
	t.AgentID = agent.ID
	f.tokens[hash] = t
	m.Agent = agent
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
	cleartext, _, err := h.service.CreateToken(context.Background(), name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h.enrollWithKey(t, cleartext)
}

func (h *harness) enrollWith(t *testing.T, cleartext string) Machine {
	t.Helper()
	enrolled, _ := h.enrollWithKey(t, cleartext)
	return enrolled
}

// enrollWithKey présente un jeton déjà émis avec une clé et un identifiant
// tirés pour l'occasion, comme l'agent.
func (h *harness) enrollWithKey(t *testing.T, cleartext string) (Machine, ed25519.PrivateKey) {
	t.Helper()
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

// prove signe une preuve neuve, comme l'agent : un aléa qu'il tire, la
// machine, l'heure.
func (h *harness) prove(t *testing.T, m Machine, private ed25519.PrivateKey, at time.Time) Proof {
	t.Helper()
	return signedBy(t, m.ID, private, NewNonce(), at)
}

func signedBy(t *testing.T, machineID string, private ed25519.PrivateKey, nonce []byte, at time.Time) Proof {
	t.Helper()
	payload, err := SignedPayload(nonce, machineID, at.Unix())
	if err != nil {
		t.Fatal(err)
	}
	return Proof{MachineID: machineID, Nonce: nonce, Timestamp: at.Unix(), Signature: Sign(private, payload)}
}

func TestCreateToken_RefusesABadName(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"", "VPS", "vps paris", "-vps", "vps-", "a.b"} {
		if _, _, err := h.service.CreateToken(context.Background(), name, nil); !errors.Is(err, ErrNameInvalid) {
			t.Errorf("%q accepted: %v", name, err)
		}
	}
	cleartext, token, err := h.service.CreateToken(context.Background(), "vps-paris-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleartext) < 40 || token.Prefix != cleartext[:9] || token.Hash != HashToken(cleartext) {
		t.Fatalf("token %q %+v", cleartext, token)
	}
}

// size compte les couples retenus.
func (n *nonces) size() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.seen)
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

// Protocole 2 : une preuve signée par l'agent ouvre un flux une fois ; la
// même, rejouée, est refusée, et l'agent en signe une autre.
func TestAuthenticate_AcceptsAFreshProofOnce(t *testing.T) {
	h := newHarness(t)
	m, private := h.enroll(t, "vps-paris-1")
	proof := h.prove(t, m, private, h.now)
	if _, err := h.service.Authenticate(context.Background(), proof); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Authenticate(context.Background(), proof); !errors.Is(err, ErrReplayed) {
		t.Fatalf("replayed proof: %v", err)
	}
	if _, err := h.service.Authenticate(context.Background(), h.prove(t, m, private, h.now)); err != nil {
		t.Fatalf("a fresh nonce: %v", err)
	}
}

// L'horloge, la clé, la machine et la forme de l'aléa se jugent ; une preuve
// signée pour une machine ne vaut pas pour une autre.
func TestAuthenticate_RefusesSkewWrongKeyForeignMachineAndBadNonce(t *testing.T) {
	h := newHarness(t)
	m, private := h.enroll(t, "vps-paris-1")
	for _, at := range []time.Time{h.now.Add(-6 * time.Minute), h.now.Add(6 * time.Minute)} {
		if _, err := h.service.Authenticate(context.Background(), h.prove(t, m, private, at)); !errors.Is(err, ErrClockSkew) {
			t.Errorf("skew %v: %v", at.Sub(h.now), err)
		}
	}
	_, wrong, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := h.service.Authenticate(context.Background(), h.prove(t, m, wrong, h.now)); !errors.Is(err, ErrBadSignature) {
		t.Errorf("wrong key: %v", err)
	}
	other, _ := h.enroll(t, "vps-lyon-2")
	foreign := h.prove(t, other, private, h.now)
	foreign.MachineID = m.ID
	if _, err := h.service.Authenticate(context.Background(), foreign); !errors.Is(err, ErrBadSignature) {
		t.Errorf("proof of another machine: %v", err)
	}
	short := signedBy(t, m.ID, private, NewNonce(), h.now)
	short.Nonce = short.Nonce[:NonceSize-1]
	if _, err := h.service.Authenticate(context.Background(), short); !errors.Is(err, ErrBadProof) {
		t.Errorf("short nonce: %v", err)
	}
}

// Qui n'a pas la clé ne brûle pas l'aléa d'un autre : le couple n'est retenu
// qu'une fois la signature vérifiée. Il est oublié quand son horodatage est
// sorti de la tolérance.
func TestAuthenticate_AWrongSignatureDoesNotBurnTheNonce(t *testing.T) {
	h := newHarness(t)
	m, private := h.enroll(t, "vps-paris-1")
	_, wrong, _ := ed25519.GenerateKey(rand.Reader)
	nonce := NewNonce()
	if _, err := h.service.Authenticate(context.Background(), signedBy(t, m.ID, wrong, nonce, h.now)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := h.service.Authenticate(context.Background(), signedBy(t, m.ID, private, nonce, h.now)); err != nil {
		t.Fatalf("the agent's proof after a forged one: %v", err)
	}
	if got := h.service.nonces.size(); got != 1 {
		t.Fatalf("%d nonces kept, want only the successful one", got)
	}
	h.now = h.now.Add(nonceMemory + nonceSweepInterval)
	if _, err := h.service.Authenticate(context.Background(), h.prove(t, m, private, h.now)); err != nil {
		t.Fatal(err)
	}
	if got := h.service.nonces.size(); got != 1 {
		t.Fatalf("%d nonces kept after the sweep, want only the newest", got)
	}
}

// L'enrôlement fige le système ; l'agent le redit à chaque connexion, et
// une montée de version se voit sans ré-enrôler. Ce qui n'est pas un nom
// de système n'écrase rien.
func TestNoteSystem_FollowsWhatTheAgentSays(t *testing.T) {
	h := newHarness(t)
	m, _ := h.enroll(t, "vps-paris-1")
	enrolledWith, _ := h.service.Get(context.Background(), m.ID)
	if err := h.service.NoteSystem(context.Background(), m.ID, "Debian GNU/Linux 13"); err != nil {
		t.Fatal(err)
	}
	if status, _ := h.service.Get(context.Background(), m.ID); status.OS != "Debian GNU/Linux 13" {
		t.Fatalf("after an upgrade: %q", status.OS)
	}
	for _, refused := range []string{"", "Debian\nGNU", "Système 12", strings.Repeat("a", MaxSystemLength+1)} {
		if err := h.service.NoteSystem(context.Background(), m.ID, refused); err != nil {
			t.Fatal(err)
		}
		if status, _ := h.service.Get(context.Background(), m.ID); status.OS != "Debian GNU/Linux 13" {
			t.Fatalf("%q replaced the system (enrolled with %q): %q", refused, enrolledWith.OS, status.OS)
		}
	}
	systems, err := h.service.Systems(context.Background())
	if err != nil || len(systems) != 1 || systems[0] != "Debian GNU/Linux 13" {
		t.Fatalf("systems %v, %v", systems, err)
	}
}

func TestConnectSignalDisconnect_DriveOnlineAndLastSeen(t *testing.T) {
	h := newHarness(t)
	m, _ := h.enroll(t, "vps-paris-1")
	if status, _ := h.service.Get(context.Background(), m.ID); status.Online {
		t.Fatal("online before any connection")
	}
	session, err := h.service.Connect(context.Background(), m, "51.15.20.114", "v0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	status, _ := h.service.Get(context.Background(), m.ID)
	if !status.Online || status.Address != "51.15.20.114" {
		t.Fatalf("after connect: %+v", status)
	}
	h.now = h.now.Add(30 * time.Second)
	if _, err := h.service.Signal(context.Background(), session.Token); err != nil {
		t.Fatal(err)
	}
	status, _ = h.service.Get(context.Background(), m.ID)
	if !status.LastSeenAt.Equal(h.now) {
		t.Fatalf("signal did not touch last seen: %+v", status)
	}
	if _, err := h.service.Signal(context.Background(), "bogus"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("bogus session: %v", err)
	}
	h.service.Disconnect(session)
	if status, _ := h.service.Get(context.Background(), m.ID); status.Online {
		t.Fatal("still online after disconnect")
	}
	if _, err := h.service.Signal(context.Background(), session.Token); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("signal after disconnect: %v", err)
	}
}

func TestRemove_ClosesTheOpenSession(t *testing.T) {
	h := newHarness(t)
	m, _ := h.enroll(t, "vps-paris-1")
	session, err := h.service.Connect(context.Background(), m, "", "", "")
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
	first, _ := sessions.Open("m", "a1", time.Now())
	second, _ := sessions.Open("m", "a2", time.Now())
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
