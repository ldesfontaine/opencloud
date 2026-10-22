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
	_, again, err := h.service.CreateReenrollToken(ctx, enrolled.ID, nil)
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
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", "observe", "", ""); err != nil {
		t.Fatal(err)
	}
	status, _ := h.service.Get(ctx, enrolled.ID)
	if FormatCapabilities(status.Agent.Capabilities) != "observe" || !status.Agent.CapabilitiesDiffer() {
		t.Fatalf("after a report without agent.update: %+v", status.Agent)
	}
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", "observe,agent.update", "", ""); err != nil {
		t.Fatal(err)
	}
	status, _ = h.service.Get(ctx, enrolled.ID)
	if status.Agent.CapabilitiesDiffer() || status.Agent.Version != "v0.0.2" {
		t.Fatalf("after the right report: %+v", status.Agent)
	}
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", "shell.exec", "", ""); err != nil {
		t.Fatalf("unreadable rights must not refuse the stream: %v", err)
	}
	status, _ = h.service.Get(ctx, enrolled.ID)
	if FormatCapabilities(status.Agent.Capabilities) != "observe,agent.update" {
		t.Fatalf("unreadable rights replaced the report: %+v", status.Agent)
	}
}

// Le compte et les droits indisponibles se redisent à chaque ouverture du flux.
// Une raison est remplacée avec les droits ; un compte qui ne se lit pas laisse
// celui d'avant ; la raison ne fait pas apparaître d'écart avec le jeton.
func TestConnect_RecordsTheAccountAndWhyARightCannotBeExercised(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cleartext, _, err := h.service.CreateToken(ctx, "vps-paris-1", []Capability{{Name: CapabilityDockerDeploy}, {Name: CapabilityAgentUpdate}})
	if err != nil {
		t.Fatal(err)
	}
	enrolled := h.enrollWith(t, cleartext)
	status, _ := h.service.Get(ctx, enrolled.ID)
	if status.Agent.RunAs != nil || status.Agent.Unavailable != nil {
		t.Fatalf("an agent that said nothing has no account: %+v", status.Agent)
	}

	const rights = "observe,agent.update,docker.deploy"
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", rights, "998:opencloud-agent", "docker.deploy=docker_denied"); err != nil {
		t.Fatal(err)
	}
	status, _ = h.service.Get(ctx, enrolled.ID)
	if status.Agent.RunAs == nil || *status.Agent.RunAs != (Account{UID: 998, Name: "opencloud-agent"}) {
		t.Fatalf("account %+v", status.Agent.RunAs)
	}
	if status.Agent.UnavailableReason(CapabilityDockerDeploy) != "docker_denied" || status.Agent.UnavailableReason(CapabilityAgentUpdate) != "" {
		t.Fatalf("unavailable %+v", status.Agent.Unavailable)
	}
	if status.Agent.CapabilitiesDiffer() {
		t.Fatal("the reason must not make a gap with the token")
	}

	// Un compte illisible laisse celui d'avant ; un en-tête de droits illisible
	// efface les raisons plutôt que d'en garder des périmées.
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", rights, "Root:0", "docker.deploy=whatever"); err != nil {
		t.Fatalf("unreadable account and reasons must not refuse the stream: %v", err)
	}
	status, _ = h.service.Get(ctx, enrolled.ID)
	if status.Agent.RunAs == nil || status.Agent.RunAs.UID != 998 {
		t.Fatalf("an unreadable account replaced the previous one: %+v", status.Agent.RunAs)
	}
	if status.Agent.Unavailable != nil {
		t.Fatalf("an unreadable header kept a stale reason: %+v", status.Agent.Unavailable)
	}

	// Un agent qui ne dit plus rien n'a plus de raison, mais garde son compte.
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", rights, "", ""); err != nil {
		t.Fatal(err)
	}
	status, _ = h.service.Get(ctx, enrolled.ID)
	if status.Agent.RunAs == nil || status.Agent.RunAs.Name != "opencloud-agent" || status.Agent.Unavailable != nil {
		t.Fatalf("after a silent report: %+v", status.Agent)
	}

	// Le compte change avec l'unité : root.
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", rights, "0:root", ""); err != nil {
		t.Fatal(err)
	}
	status, _ = h.service.Get(ctx, enrolled.ID)
	if status.Agent.RunAs == nil || !status.Agent.RunAs.IsRoot() {
		t.Fatalf("account after the move to root: %+v", status.Agent.RunAs)
	}
}

// Une raison pour un droit que l'agent ne rapporte pas ne s'écrit nulle part ;
// sans droits rapportés, aucune raison ne s'écrit.
func TestConnect_IgnoresAReasonForARightTheAgentDoesNotReport(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cleartext, _, err := h.service.CreateToken(ctx, "vps-paris-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	enrolled := h.enrollWith(t, cleartext)
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", "observe", "", "files.write=path_denied"); err != nil {
		t.Fatal(err)
	}
	status, _ := h.service.Get(ctx, enrolled.ID)
	if len(status.Agent.Unavailable) != 0 {
		t.Fatalf("a reason for a right nobody reported: %+v", status.Agent.Unavailable)
	}
	if _, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.2", "", "", "files.write=path_denied"); err != nil {
		t.Fatal(err)
	}
	status, _ = h.service.Get(ctx, enrolled.ID)
	if len(status.Agent.Unavailable) != 0 {
		t.Fatalf("reasons without a report of rights: %+v", status.Agent.Unavailable)
	}
}

// L'empreinte de la clé du serveur que l'agent redit se garde telle quelle ;
// ce qui n'a pas la forme d'une empreinte s'écrit vide.
func TestNoteServerKey_KeepsAFingerprintAndNothingElse(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cleartext, _, err := h.service.CreateToken(ctx, "vps-paris-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	enrolled := h.enrollWith(t, cleartext)
	const fingerprint = "SHA256:Zmh6rfhivXdsj8GLjp+OIAiXFIVu4jOzkCpZHQ1fKSU"
	for _, c := range []struct{ reported, kept string }{
		{fingerprint, fingerprint},
		{"SHA256:court", ""},
		{"MD5:" + fingerprint[7:], ""},
		{"", ""},
	} {
		if err := h.service.NoteServerKey(ctx, enrolled.Agent.ID, c.reported); err != nil {
			t.Fatal(err)
		}
		status, _ := h.service.Get(ctx, enrolled.ID)
		if status.Agent.ServerKeyFingerprint != c.kept {
			t.Errorf("%q kept as %q", c.reported, status.Agent.ServerKeyFingerprint)
		}
	}
}

// La racine que l'agent redit se garde si c'est un chemin de droit ; sinon la
// base garde ce qu'elle avait.
func TestNoteRootDir_KeepsAValidPathAndIgnoresTheRest(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cleartext, _, err := h.service.CreateToken(ctx, "vps-paris-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	enrolled := h.enrollWith(t, cleartext)
	for _, c := range []struct{ reported, kept string }{
		{"/srv/apps", "/srv/apps"},
		{"", "/srv/apps"},
		{"srv/relatif", "/srv/apps"},
		{"/srv/../etc", "/srv/apps"},
		{"/srv/espace ici", "/srv/apps"},
		{"/", "/srv/apps"},
		{"/srv/workspace", "/srv/workspace"},
	} {
		if err := h.service.NoteRootDir(ctx, enrolled.Agent.ID, c.reported); err != nil {
			t.Fatal(err)
		}
		status, _ := h.service.Get(ctx, enrolled.ID)
		if status.Agent.RootDir != c.kept {
			t.Errorf("%q kept as %q, want %q", c.reported, status.Agent.RootDir, c.kept)
		}
	}
}

// Révoquer : le flux tombe, la preuve suivante est refusée (l'agent
// s'arrête), les jetons de ré-enrôlement en attente partent ; la machine
// reste. Ré-enrôler installe un nouvel agent et lève la révocation.
func TestRevoke_RefusesTheAgentClosesItsSessionAndReenrollLiftsIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	m, private := h.enroll(t, "vps-paris-1")
	session, err := h.service.Connect(ctx, m, "192.0.2.1", "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.service.CreateReenrollToken(ctx, m.ID, nil); err != nil {
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

	cleartext, _, err := h.service.CreateReenrollToken(ctx, m.ID, nil)
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

type lostAlerter struct {
	lost []string
	back []string
}

func (a *lostAlerter) MachineLost(_ context.Context, machine Machine) {
	a.lost = append(a.lost, machine.ID)
}
func (a *lostAlerter) MachineBack(_ context.Context, machineID string) {
	a.back = append(a.back, machineID)
}
func (a *lostAlerter) MachineRemoved(context.Context, string) {}
