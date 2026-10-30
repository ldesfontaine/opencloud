package action_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/release"
	"github.com/ldesfontaine/opencloud/internal/release/releasetest"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// updateBench branche le composant comme serve : une machine en ligne dont
// l'agent annonce une version, une bibliothèque de versions importées, et les
// colis. La clé de version est tirée ici, jamais celle du binaire.
type updateBench struct {
	db       *store.DB
	service  *action.Service
	machines *machine.Service
	pusher   *pusher
	state    *os.Root
	library  *release.Library
	key      release.PublicKey
	private  release.PrivateKey
	session  *machine.Session
}

type updateOptions struct {
	rights       string
	agentVersion string
	arch         string
	// Les droits que l'agent dit ne pas pouvoir exercer (l'en-tête).
	unavailable string
	// L'agent ne rapporte pas de retour arrière (non migré).
	noRollback bool
}

func newUpdateBench(t *testing.T, options updateOptions) *updateBench {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	state, db := storetest.OpenWithStateDir(t)
	b := &updateBench{db: db, state: state}
	b.machines = machine.New(db, machine.NewSessions(), logger)
	enrolled := enrollWithArch(t, b.machines, options.arch)
	session, err := b.machines.Connect(context.Background(), enrolled, "192.0.2.1", options.agentVersion, options.rights, "", options.unavailable)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.machines.Disconnect(session) })
	b.session = session
	// Un agent migré dit à chaque ouverture de flux qu'il revient seul en
	// arrière ; sans cela le serveur ne lui envoie aucune mise à jour.
	if !options.noRollback {
		if err := b.machines.NoteRollbackVersion(context.Background(), enrolled.Agent.ID, "1"); err != nil {
			t.Fatal(err)
		}
	}
	b.key, b.private, err = release.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	b.library = release.NewLibrary(state, b.key)
	outbox, err := action.NewOutbox(state)
	if err != nil {
		t.Fatal(err)
	}
	b.pusher = &pusher{db: db}
	b.service = action.New(db, b.machines, logger)
	b.service.SetAuditor(audit.New(db, logger))
	b.service.SetKey(action.GenerateKey())
	b.service.SetCommander(b.pusher)
	b.service.SetOutbox(outbox)
	return b
}

// enrollWithArch enrôle la machine de test avec l'architecture donnée ; vide,
// la machine n'en a pas dit.
func enrollWithArch(t *testing.T, machines *machine.Service, arch string) machine.Machine {
	t.Helper()
	ctx := context.Background()
	cleartext, _, err := machines.CreateToken(ctx, "vps-paris-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := machines.Enroll(ctx, machine.Enrollment{
		MachineID: machineID, PublicKey: public, Token: cleartext,
		Hostname: "vps-paris-1", Address: "51.15.20.114", OS: "Debian 12", Arch: arch, AgentVersion: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return enrolled
}

// publish signe une version avec la clé de test et l'importe : ce que
// l'opérateur fait avec `release import`.
func (b *updateBench) publish(t *testing.T, version string, binaries map[string][]byte) {
	t.Helper()
	if _, err := b.library.Import(releasetest.Write(t, b.private, version, binaries)); err != nil {
		t.Fatal(err)
	}
}

func (b *updateBench) publishBoth(t *testing.T, version string) {
	t.Helper()
	b.publish(t, version, releasetest.Binaries(version, "amd64", "arm64"))
}

func (b *updateBench) offer(t *testing.T) action.UpdateOffer {
	t.Helper()
	offer, err := b.service.OfferUpdate(operator(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	return offer
}

func TestOfferUpdate_Reasons(t *testing.T) {
	const withRight = "observe,agent.update"
	cases := []struct {
		name    string
		reason  string
		prepare func(t *testing.T) *updateBench
	}{
		{"available", "", func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.1", arch: "amd64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			return b
		}},
		{"an agent that reported nothing is judged by itself", "", func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: "", agentVersion: "v0.0.1", arch: "arm64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			return b
		}},
		{action.OfferNoReleases, action.OfferNoReleases, func(t *testing.T) *updateBench {
			return newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.1", arch: "amd64"})
		}},
		{"an unreadable server version", action.OfferNoReleases, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.1", arch: "amd64"})
			b.service.SetReleases(b.library, "dev")
			return b
		}},
		{action.OfferNoAgent, action.OfferNoAgent, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.1", arch: "amd64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			if _, err := b.db.RevokeAgent(context.Background(), machineID, benchNow); err != nil {
				t.Fatal(err)
			}
			return b
		}},
		{action.OfferOffline, action.OfferOffline, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.1", arch: "amd64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			b.machines.Disconnect(b.session)
			return b
		}},
		{action.OfferRightMissing, action.OfferRightMissing, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: "observe", agentVersion: "v0.0.1", arch: "amd64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			return b
		}},
		{action.OfferArchUnknown, action.OfferArchUnknown, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.1", arch: ""})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			return b
		}},
		{action.OfferSameVersion, action.OfferSameVersion, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.2", arch: "amd64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			return b
		}},
		{action.OfferNewerAgent, action.OfferNewerAgent, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.1.0", arch: "amd64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			return b
		}},
		{"an unreadable agent version counts as older", "", func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "dev", arch: "amd64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			return b
		}},
		{action.OfferNoRelease, action.OfferNoRelease, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.1", arch: "amd64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.3")
			return b
		}},
		{"no binary for this arch", action.OfferNoRelease, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.1", arch: "riscv64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			return b
		}},
		{action.OfferReleaseInvalid, action.OfferReleaseInvalid, func(t *testing.T) *updateBench {
			b := newUpdateBench(t, updateOptions{rights: withRight, agentVersion: "v0.0.1", arch: "amd64"})
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			damage(t, b, "releases/v0.0.2/opencloud-linux-amd64")
			return b
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			offer := c.prepare(t).offer(t)
			if offer.Reason != c.reason || offer.Available != (c.reason == "") {
				t.Fatalf("offer %+v, want reason %q", offer, c.reason)
			}
		})
	}
}

// damage change un octet d'un fichier importé, comme un disque abîmé.
func damage(t *testing.T, b *updateBench, name string) {
	t.Helper()
	if err := b.state.WriteFile(name, []byte("not what was signed"), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestOfferUpdate_SaysWhatItKnows(t *testing.T) {
	b := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "arm64"})
	b.publishBoth(t, "v0.0.2")
	b.service.SetReleases(b.library, "v0.0.2")
	want := action.UpdateOffer{Current: "v0.0.1", Target: "v0.0.2", Arch: "arm64", Available: true}
	if offer := b.offer(t); offer != want {
		t.Fatalf("offer %+v, want %+v", offer, want)
	}
	if _, err := b.service.OfferUpdate(operator(), "11111111-2222-4333-8444-000000000000"); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("unknown machine: %v", err)
	}
}

// Le compte de l'agent ne peut pas exercer agent.update (bin/ n'est pas à lui) :
// la fiche le dit, l'offre reste disponible et l'ordre part. Le serveur ne
// refuse jamais d'avance sur la foi d'un rapport qui date de l'ouverture du
// flux ; l'agent refuse au moment de l'ordre, avec sa raison.
func TestUpdateAgent_AnUnavailableRightIsShownButNeverRefusedByTheServer(t *testing.T) {
	b := newUpdateBench(t, updateOptions{
		rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64", unavailable: "agent.update=path_denied",
	})
	b.publishBoth(t, "v0.0.2")
	b.service.SetReleases(b.library, "v0.0.2")
	want := action.UpdateOffer{Current: "v0.0.1", Target: "v0.0.2", Arch: "amd64", Available: true, Reason: action.OfferRightUnavailable}
	if offer := b.offer(t); offer != want {
		t.Fatalf("offer %+v, want %+v", offer, want)
	}
	run, err := b.service.UpdateAgent(operator(), machineID)
	if err != nil || run.Kind != agentwire.KindAgentUpdate || len(b.pusher.sent) != 1 {
		t.Fatalf("the order must leave: %+v %v / %+v", run, err, b.pusher.sent)
	}
	// Une raison pour un autre droit ne touche pas l'offre.
	other := newUpdateBench(t, updateOptions{
		rights: "observe,agent.update,docker.deploy", agentVersion: "v0.0.1", arch: "amd64", unavailable: "docker.deploy=docker_denied",
	})
	other.publishBoth(t, "v0.0.2")
	other.service.SetReleases(other.library, "v0.0.2")
	if offer := other.offer(t); offer.Reason != "" || !offer.Available {
		t.Fatalf("another right's reason leaked into the offer: %+v", offer)
	}
}

func TestUpdateAgent_SendsTheVerifiedBinaryOfItsArch(t *testing.T) {
	b := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "arm64"})
	b.publishBoth(t, "v0.0.2")
	b.service.SetReleases(b.library, "v0.0.2")

	run, err := b.service.UpdateAgent(operator(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Kind != agentwire.KindAgentUpdate || run.State != action.StateSent {
		t.Fatalf("run %+v", run)
	}
	if len(b.pusher.sent) != 1 || b.pusher.sent[0].Kind != agentwire.KindAgentUpdate {
		t.Fatalf("sent %+v", b.pusher.sent)
	}
	var body agentwire.OrderBody
	var params agentwire.AgentUpdateParams
	if json.Unmarshal(b.pusher.sent[0].Order.Body, &body) != nil || json.Unmarshal(body.Params, &params) != nil {
		t.Fatalf("order %s", b.pusher.sent[0].Order.Body)
	}
	if params.Version != "v0.0.2" || params.Binary != "opencloud-linux-arm64" || len(params.Files) != 3 {
		t.Fatalf("params %+v", params)
	}
	if string(run.Params) != string(body.Params) {
		t.Fatalf("the run does not keep the params: %s", run.Params)
	}
	// Chaque fichier du colis a la somme que l'ordre signe, et l'agent peut le
	// télécharger : c'est ceux de la version importée.
	wantContent := map[string]string{
		"opencloud-linux-arm64": "the arm64 binary of v0.0.2",
	}
	for _, file := range params.Files {
		opened, size, err := b.service.OpenParcel(machineID, params.Parcel, file.Name)
		if err != nil {
			t.Fatalf("%s: %v", file.Name, err)
		}
		content, err := io.ReadAll(opened)
		_ = opened.Close()
		digest := sha256.Sum256(content)
		if err != nil || size != file.Size || int64(len(content)) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 {
			t.Fatalf("%s: size %d, %d bytes, %v", file.Name, size, len(content), err)
		}
		if want, named := wantContent[file.Name]; named && string(content) != want {
			t.Fatalf("%s holds %q", file.Name, content)
		}
	}
	names := []string{params.Files[0].Name, params.Files[1].Name, params.Files[2].Name}
	if names[0] != brand.ReleaseSumsFile || names[1] != brand.ReleaseSignatureFile || names[2] != "opencloud-linux-arm64" {
		t.Fatalf("files %v", names)
	}
	// Le parcel n'est lisible que par la machine à laquelle il est destiné.
	if _, _, err := b.service.OpenParcel("another-machine", params.Parcel, "opencloud-linux-arm64"); !errors.Is(err, action.ErrParcelNotFound) {
		t.Fatalf("another machine: %v", err)
	}
	// La bibliothèque n'a pas bougé : emprunter n'efface rien.
	if _, err := b.library.Artifact(release.Version{Minor: 0, Patch: 2}, "arm64"); err != nil {
		t.Fatalf("the library lost its file: %v", err)
	}
}

func TestUpdateAgent_RefusesWhenTheImportedFileChanged(t *testing.T) {
	b := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64"})
	b.publishBoth(t, "v0.0.2")
	b.service.SetReleases(b.library, "v0.0.2")
	damage(t, b, "releases/v0.0.2/opencloud-linux-amd64")

	if _, err := b.service.UpdateAgent(operator(), machineID); !errors.Is(err, action.ErrUpdateUnavailable) {
		t.Fatalf("a changed binary: %v", err)
	}
	runs, err := b.db.ListActionRuns(context.Background(), action.Query{Limit: 10})
	if err != nil || len(runs) != 0 || len(b.pusher.sent) != 0 {
		t.Fatalf("written or sent: %+v %+v %v", runs, b.pusher.sent, err)
	}
}

func TestUpdateAgent_SaysWhyItCannotLeave(t *testing.T) {
	cases := map[string]struct {
		options updateOptions
		prepare func(b *updateBench)
		want    error
	}{
		"offline":                   {updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64"}, func(b *updateBench) { b.machines.Disconnect(b.session) }, action.ErrMachineOffline},
		"the agent lacks the right": {updateOptions{rights: "observe", agentVersion: "v0.0.1", arch: "amd64"}, func(*updateBench) {}, action.ErrRightMissing},
		"already up to date":        {updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.2", arch: "amd64"}, func(*updateBench) {}, action.ErrUpdateUnavailable},
		"no release imported for it": {updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64"}, func(b *updateBench) {
			b.service.SetReleases(b.library, "v0.0.3")
		}, action.ErrUpdateUnavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := newUpdateBench(t, c.options)
			b.publishBoth(t, "v0.0.2")
			b.service.SetReleases(b.library, "v0.0.2")
			c.prepare(b)
			if _, err := b.service.UpdateAgent(operator(), machineID); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if len(b.pusher.sent) != 0 {
				t.Fatalf("sent %+v", b.pusher.sent)
			}
		})
	}
}

func TestUpdateAgent_WithoutALibraryOrParcelsSendsNothing(t *testing.T) {
	b := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64"})
	if _, err := b.service.UpdateAgent(operator(), machineID); !errors.Is(err, action.ErrUpdateUnavailable) {
		t.Fatalf("no library: %v", err)
	}
	b.publishBoth(t, "v0.0.2")
	b.service.SetReleases(b.library, "v0.0.2")
	b.service.SetOutbox(nil)
	if _, err := b.service.UpdateAgent(operator(), machineID); !errors.Is(err, action.ErrUpdateUnavailable) {
		t.Fatalf("no outbox: %v", err)
	}
}

func TestUpdateAgent_AnOrderThatDoesNotLeaveDiscardsItsParcel(t *testing.T) {
	b := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64"})
	b.publishBoth(t, "v0.0.2")
	b.service.SetReleases(b.library, "v0.0.2")
	b.pusher.refuse = errors.New("stream fell")
	run, err := b.service.UpdateAgent(operator(), machineID)
	if err != nil || run.State != action.StateFailed || run.Reason != action.ReasonUndelivered {
		t.Fatalf("run %+v %v", run, err)
	}
	var params agentwire.AgentUpdateParams
	if err := json.Unmarshal(run.Params, &params); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.service.OpenParcel(machineID, params.Parcel, params.Binary); !errors.Is(err, action.ErrParcelNotFound) {
		t.Fatalf("the parcel of an order that never left is still served: %v", err)
	}
	if _, err := b.library.Artifact(release.Version{Patch: 2}, "amd64"); err != nil {
		t.Fatalf("the library lost its file: %v", err)
	}
}

func TestConfirmAgentUpdate_SucceedsOnReconnectWithTheTargetVersionOnly(t *testing.T) {
	b := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64"})
	b.publishBoth(t, "v0.0.2")
	b.service.SetReleases(b.library, "v0.0.2")
	ctx := operator()
	run, err := b.service.UpdateAgent(ctx, machineID)
	if err != nil {
		t.Fatal(err)
	}
	found, err := b.db.GetMachine(ctx, machineID)
	if err != nil {
		t.Fatal(err)
	}
	// Accepté : l'agent télécharge et s'éprouve ; l'ordre reste ouvert.
	accepted := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionAccepted}}}
	if err := b.service.Record(context.Background(), machineID, accepted); err != nil {
		t.Fatal(err)
	}
	runState := func() action.Run {
		t.Helper()
		got, err := b.db.GetActionRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	// Il se reconnecte avec l'ancienne version (relancé sans avoir basculé),
	// avec une version vide, avec une autre : l'ordre reste ouvert.
	for _, reported := range []string{"v0.0.1", "", "v0.0.3"} {
		if err := b.service.ConfirmAgentUpdate(ctx, machineID, found.Agent.ID, reported); err != nil {
			t.Fatal(err)
		}
		if got := runState(); got.State != action.StateAccepted {
			t.Fatalf("reported %q closed the order: %+v", reported, got)
		}
	}
	// Un autre agent qui annonce la bonne version ne ferme pas l'ordre.
	if err := b.service.ConfirmAgentUpdate(ctx, machineID, "another-agent", "v0.0.2"); err != nil {
		t.Fatal(err)
	}
	if got := runState(); got.State != action.StateAccepted {
		t.Fatalf("another agent closed the order: %+v", got)
	}
	if err := b.service.ConfirmAgentUpdate(ctx, machineID, found.Agent.ID, "v0.0.2"); err != nil {
		t.Fatal(err)
	}
	got := runState()
	if got.State != action.StateSucceeded || got.FinishedAt.IsZero() || len(got.Output) != 1 || got.Output[0].Line != "reconnected as v0.0.2" {
		t.Fatalf("after the agent came back with the target version: %+v", got)
	}
}
