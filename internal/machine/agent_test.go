package machine

import (
	"context"
	"errors"
	"testing"
	"time"
)

var testReportedAt = time.Unix(1_800_000_000, 0)

// Le jeton porte les droits cochés ; le ré-enrôlement reprend ceux que le
// jeton de l'agent courant demandait.
func TestCreateToken_CarriesTheCheckedRightsAndReenrollKeepsThem(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	checked := []Capability{{Name: CapabilityFilesWrite, Paths: []string{"/srv/workspace"}}, {Name: CapabilityDockerDeploy}}
	cleartext, token, err := h.service.CreateToken(ctx, "vps-paris-1", checked)
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatCapabilities(token.Capabilities); got != "observe,docker.deploy,files.write=/srv/workspace" {
		t.Fatalf("token rights %q", got)
	}
	if _, _, err := h.service.CreateToken(ctx, "vps-lyon-2", []Capability{{Name: "shell.exec"}}); !errors.Is(err, ErrCapabilityUnknown) {
		t.Fatalf("unknown right: %v", err)
	}
	enrolled := h.enrollWith(t, cleartext)
	if !SameCapabilities(enrolled.Agent.Requested, token.Capabilities) {
		t.Fatalf("the agent must know what its token asked: %+v", enrolled.Agent)
	}
	_, again, err := h.service.CreateReenrollToken(ctx, enrolled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !SameCapabilities(again.Capabilities, token.Capabilities) {
		t.Fatalf("re-enroll rights %+v", again.Capabilities)
	}
}

// L'agent rapporte ses droits à l'ouverture du flux ; ce qui ne se lit pas
// n'écrase rien, et le flux s'ouvre quand même.
func TestConnect_RecordsTheReportedRightsAndTheGapShows(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cleartext, _, err := h.service.CreateToken(ctx, "vps-paris-1", []Capability{{Name: CapabilityAgentUpdate}})
	if err != nil {
		t.Fatal(err)
	}
	enrolled := h.enrollWith(t, cleartext)
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", "observe"); err != nil {
		t.Fatal(err)
	}
	status, _ := h.service.Get(ctx, enrolled.ID)
	if FormatCapabilities(status.Agent.Capabilities) != "observe" || !status.Agent.CapabilitiesDiffer() {
		t.Fatalf("after a report without agent.update: %+v", status.Agent)
	}
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", "observe,agent.update"); err != nil {
		t.Fatal(err)
	}
	status, _ = h.service.Get(ctx, enrolled.ID)
	if status.Agent.CapabilitiesDiffer() || status.Agent.Version != "v0.0.2" {
		t.Fatalf("after the right report: %+v", status.Agent)
	}
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", "shell.exec"); err != nil {
		t.Fatalf("unreadable rights must not refuse the stream: %v", err)
	}
	status, _ = h.service.Get(ctx, enrolled.ID)
	if FormatCapabilities(status.Agent.Capabilities) != "observe,agent.update" {
		t.Fatalf("unreadable rights replaced the report: %+v", status.Agent)
	}
}

// Révoquer : le flux tombe, la preuve suivante est refusée (l'agent
// s'arrête), les jetons de ré-enrôlement en attente partent ; la machine
// reste. Ré-enrôler installe un nouvel agent et lève la révocation.
func TestRevoke_RefusesTheAgentClosesItsSessionAndReenrollLiftsIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	m, private := h.enroll(t, "vps-paris-1")
	session, err := h.service.Connect(ctx, m, "192.0.2.1", "v0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.service.CreateReenrollToken(ctx, m.ID); err != nil {
		t.Fatal(err)
	}

	if err := h.service.Revoke(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("the revoked agent's session is still open")
	}
	if pending, _ := h.service.PendingTokens(ctx); len(pending) != 0 {
		t.Fatalf("pending re-enroll tokens survived the revocation: %+v", pending)
	}
	if _, err := h.service.Authenticate(ctx, h.prove(t, m, private, h.now)); !errors.Is(err, ErrAgentRevoked) {
		t.Fatalf("revoked agent: %v", err)
	}
	status, err := h.service.Get(ctx, m.ID)
	if err != nil || !status.Agent.IsRevoked() || status.Online {
		t.Fatalf("the machine stays, its agent revoked: %+v %v", status, err)
	}
	if err := h.service.Revoke(ctx, m.ID); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("revoking twice: %v", err)
	}
	// Sans agent actif, la machine n'est pas « perdue » : rien à perdre.
	alerts := &lostAlerter{}
	h.service.SetAlerter(alerts)
	h.now = h.now.Add(time.Hour)
	if err := h.service.CheckLost(ctx); err != nil || len(alerts.lost) != 0 {
		t.Fatalf("a revoked agent raised a lost machine: %v %v", alerts.lost, err)
	}

	cleartext, _, err := h.service.CreateReenrollToken(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	reenrolled, newKey := h.enrollWithKey(t, cleartext)
	if reenrolled.ID != m.ID || !reenrolled.Agent.IsActive() || reenrolled.Agent.ID == m.Agent.ID {
		t.Fatalf("a new agent on the same machine: %+v", reenrolled)
	}
	if _, err := h.service.Authenticate(ctx, h.prove(t, reenrolled, newKey, h.now)); err != nil {
		t.Fatalf("the new agent: %v", err)
	}
}

func TestRevoke_RefusesTheLocalMachine(t *testing.T) {
	h := newHarness(t)
	if err := h.service.EnsureLocal(context.Background(), LocalInfo{Hostname: "oc"}); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Revoke(context.Background(), LocalID); !errors.Is(err, ErrLocalMachine) {
		t.Fatalf("revoke local: %v", err)
	}
}

type lostAlerter struct {
	lost []string
}

func (a *lostAlerter) MachineLost(_ context.Context, machine Machine) {
	a.lost = append(a.lost, machine.ID)
}
func (a *lostAlerter) MachineBack(context.Context, string)    {}
func (a *lostAlerter) MachineRemoved(context.Context, string) {}
