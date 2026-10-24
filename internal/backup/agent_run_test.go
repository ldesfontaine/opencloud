package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	sourceRights = "backup.produce=proxmox-vzdump"
	// Le « en clair » d'une machine virtuelle : ce que le serveur ne doit jamais
	// voir, l'archive étant chiffrée avant de quitter la source.
	clearPrefix = "contenu en clair de la machine "
)

// connectSource met la machine « pve » en ligne, avec son agent et ses droits.
func (b *bench) connectSource(rights string) {
	b.t.Helper()
	source, err := b.machines.Get(context.Background(), sourceID)
	if errors.Is(err, machine.ErrNotFound) {
		b.withSource()
		source, err = b.machines.Get(context.Background(), sourceID)
	}
	if err != nil {
		b.t.Fatal(err)
	}
	session, err := b.machines.Connect(context.Background(), source.Machine, "192.0.2.2", currentAgentVersion, rights, "", "")
	if err != nil {
		b.t.Fatal(err)
	}
	b.t.Cleanup(func() { b.machines.Disconnect(session) })
}

// reconnectDestination refait l'agent de la destination, avec cette version.
func (b *bench) reconnectDestination(version, reportedRights string) {
	b.t.Helper()
	destination, err := b.machines.Get(context.Background(), machineID)
	if err != nil {
		b.t.Fatal(err)
	}
	b.machines.Disconnect(b.session)
	b.session, err = b.machines.Connect(context.Background(), destination.Machine, "192.0.2.1", version, reportedRights, "", "")
	if err != nil {
		b.t.Fatal(err)
	}
}

// agentPlanFor est un plan agent de sujet « pve » : ses fichiers s'appellent
// pve-vm-<numéro>-AAAAMMJJ-HHMMSS.tar.age.
func agentPlanFor(destinationID, selector string, keep int) backup.PlanDefinition {
	definition := agentPlan("pve", destinationID)
	definition.Selector, definition.Keep = selector, keep
	return definition
}

// readyAgent pose ce qu'il faut pour qu'un plan agent parte : la clé, la
// destination (v0.2.0, qui connaît le préfixe), la source en ligne, le plan.
func (b *bench) readyAgent(selector string, keep int) backup.Destination {
	b.t.Helper()
	b.connectSource(sourceRights)
	_, destination := b.ready()
	if _, err := b.backups.SavePlan(operator(), agentPlanFor(destination.ID, selector, keep)); err != nil {
		b.t.Fatal(err)
	}
	return destination
}

// fakeGuests tient lieu de l'inventaire Proxmox d'une machine.
type fakeGuests map[string][]int

func (g fakeGuests) ListProxmoxGuestIDs(_ context.Context, machineID string) ([]int, error) {
	return g[machineID], nil
}

// agents joue les deux agents d'un passage : la source produit et téléverse
// une archive chiffrée, la destination la reçoit puis élague. Chaque ordre
// est terminé à l'instant où il part (Record), sans attente ni agent réel ;
// les écarts (échec, refus, ordre qui ne finit pas) se règlent par cible.
type agents struct {
	t *testing.T
	b *bench

	mu sync.Mutex
	// steps dit ce qui est parti, dans l'ordre : « produce 100 », « receive
	// 100 », « prune pve-vm-100 » ; machines, vers qui ; live, combien de
	// colis vivaient à ce moment-là.
	steps    []string
	machines []string
	live     []int
	prunes   []agentwire.BackupPruneParams
	// delivered garde ce que la destination a pu télécharger, par cible.
	delivered map[string]map[string][]byte
	parcels   map[string]string
	pending   map[string]pendingProduce

	failProduce   map[string]string // cible -> raison, état failed
	refuseProduce map[string]string // cible -> raison, état refused
	noArchive     map[string]bool   // cible -> l'agent dit réussir sans rien envoyer
	extraUpload   map[string]string // cible -> un nom de plus que l'agent téléverse
	stuck         map[string]bool   // cible -> l'ordre est accepté et ne finit pas
	refuseReceive map[string]string // cible -> raison, état refused
	failPrune     map[string]string // préfixe -> raison, état failed
	// beforeReceive est joué quand l'ordre de réception d'une cible part.
	beforeReceive func(target string)
	// afterStuck est joué quand un ordre accepté ne finira pas.
	afterStuck func(target string)
}

type pendingProduce struct {
	runID  string
	params agentwire.BackupProduceParams
}

func playAgents(t *testing.T, b *bench) *agents {
	t.Helper()
	a := &agents{
		t: t, b: b,
		delivered: map[string]map[string][]byte{}, parcels: map[string]string{}, pending: map[string]pendingProduce{},
		failProduce: map[string]string{}, refuseProduce: map[string]string{}, noArchive: map[string]bool{}, extraUpload: map[string]string{},
		stuck: map[string]bool{}, refuseReceive: map[string]string{}, failPrune: map[string]string{},
	}
	b.commands.onOrder = a.play
	return a
}

func (a *agents) play(machineID string, order agentwire.OrderBody) {
	switch order.Kind {
	case agentwire.KindBackupProduce:
		a.produce(machineID, order)
	case agentwire.KindBackupReceive:
		a.receive(machineID, order)
	case agentwire.KindBackupPrune:
		a.prune(machineID, order)
	}
}

// liveParcels compte les dossiers de colis que le serveur garde.
func (a *agents) liveParcels() int {
	entries, err := os.ReadDir(a.b.stateDir.Name() + "/outgoing")
	if err != nil {
		return 0
	}
	return len(entries)
}

func (a *agents) note(machineID, step string) {
	a.steps = append(a.steps, step)
	a.machines = append(a.machines, machineID)
	a.live = append(a.live, a.liveParcels())
}

func (a *agents) finish(machineID, runID string, state agentwire.ActionState, reason string) {
	a.t.Helper()
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: runID, State: state, Reason: reason}}}
	if err := a.b.actions.Record(context.Background(), machineID, report); err != nil {
		a.t.Errorf("record %s: %v", state, err)
	}
}

func (a *agents) produce(machineID string, order agentwire.OrderBody) {
	var params agentwire.BackupProduceParams
	if err := json.Unmarshal(order.Params, &params); err != nil {
		a.t.Errorf("produce params: %v", err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.note(machineID, "produce "+params.Target)
	a.parcels[params.Parcel] = params.Target
	switch {
	case a.stuck[params.Target]:
		a.pending[params.Target] = pendingProduce{runID: order.RunID, params: params}
		a.finish(machineID, order.RunID, agentwire.ActionAccepted, "")
		if a.afterStuck != nil {
			a.afterStuck(params.Target)
		}
	case a.refuseProduce[params.Target] != "":
		a.finish(machineID, order.RunID, agentwire.ActionRefused, a.refuseProduce[params.Target])
	case a.failProduce[params.Target] != "":
		a.finish(machineID, order.RunID, agentwire.ActionFailed, a.failProduce[params.Target])
	default:
		a.complete(machineID, order.RunID, params)
	}
}

// complete fait ce que l'agent producteur fait quand tout va bien : une
// archive chiffrée pour la clé publique du serveur, téléversée, puis succès.
func (a *agents) complete(machineID, runID string, params agentwire.BackupProduceParams) {
	a.t.Helper()
	if !a.noArchive[params.Target] {
		a.upload(machineID, params, agentwire.BackupFileName(params.Prefix, benchNow))
	}
	if extra := a.extraUpload[params.Target]; extra != "" {
		a.upload(machineID, params, extra)
	}
	a.finish(machineID, runID, agentwire.ActionSucceeded, "")
}

func (a *agents) upload(machineID string, params agentwire.BackupProduceParams, name string) {
	a.t.Helper()
	recipient, err := age.ParseX25519Recipient(params.Recipient)
	if err != nil {
		a.t.Errorf("recipient: %v", err)
		return
	}
	var sealed bytes.Buffer
	writer, err := age.Encrypt(&sealed, recipient)
	if err != nil {
		a.t.Errorf("encrypt: %v", err)
		return
	}
	if _, err := io.WriteString(writer, clearPrefix+params.Target); err != nil {
		a.t.Errorf("write: %v", err)
	}
	if err := writer.Close(); err != nil {
		a.t.Errorf("close: %v", err)
	}
	sum := sha256.Sum256(sealed.Bytes())
	if err := a.b.outbox.Receive(machineID, params.Parcel, name, bytes.NewReader(sealed.Bytes()), int64(sealed.Len()), hex.EncodeToString(sum[:])); err != nil {
		a.t.Errorf("upload %s: %v", name, err)
	}
}

// release termine un ordre produce resté en suspens, comme si l'agent venait
// de finir.
func (a *agents) release(machineID, target string) {
	a.t.Helper()
	a.mu.Lock()
	pending, found := a.pending[target]
	a.mu.Unlock()
	if !found {
		a.t.Fatalf("no produce waits for %s", target)
	}
	a.complete(machineID, pending.runID, pending.params)
}

func (a *agents) receive(machineID string, order agentwire.OrderBody) {
	var params agentwire.BackupReceiveParams
	if err := json.Unmarshal(order.Params, &params); err != nil {
		a.t.Errorf("receive params: %v", err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	target, produced := a.parcels[params.Parcel]
	if !produced {
		// Le colis de la base : rien à jouer de plus.
		a.finish(machineID, order.RunID, agentwire.ActionSucceeded, "")
		return
	}
	a.note(machineID, "receive "+target)
	files := map[string][]byte{}
	for _, entry := range params.Files {
		content, err := a.download(machineID, params.Parcel, entry)
		if err != nil {
			a.t.Errorf("download %s: %v", entry.Name, err)
			continue
		}
		files[entry.Name] = content
	}
	a.delivered[target] = files
	if a.beforeReceive != nil {
		a.beforeReceive(target)
	}
	if reason := a.refuseReceive[target]; reason != "" {
		a.finish(machineID, order.RunID, agentwire.ActionRefused, reason)
		return
	}
	a.finish(machineID, order.RunID, agentwire.ActionSucceeded, "")
}

// download lit un fichier du colis comme l'agent de la destination : par le
// serveur, avec la taille et la somme que l'ordre signe.
func (a *agents) download(machineID, parcelID string, entry agentwire.ParcelFile) ([]byte, error) {
	file, size, err := a.b.outbox.Open(machineID, parcelID, entry.Name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(content)
	if int64(len(content)) != size || size != entry.Size || hex.EncodeToString(sum[:]) != entry.SHA256 {
		return nil, fmt.Errorf("%s does not match what the order signs", entry.Name)
	}
	return content, nil
}

func (a *agents) prune(machineID string, order agentwire.OrderBody) {
	var params agentwire.BackupPruneParams
	if err := json.Unmarshal(order.Params, &params); err != nil {
		a.t.Errorf("prune params: %v", err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.note(machineID, "prune "+params.Prefix)
	a.prunes = append(a.prunes, params)
	if reason := a.failPrune[params.Prefix]; reason != "" {
		a.finish(machineID, order.RunID, agentwire.ActionFailed, reason)
		return
	}
	a.finish(machineID, order.RunID, agentwire.ActionSucceeded, "")
}

func (a *agents) summary() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.steps, ", ")
}

func (b *bench) plan(subject string) backup.Plan {
	b.t.Helper()
	plan, err := b.db.GetBackupPlan(context.Background(), subject)
	if err != nil {
		b.t.Fatal(err)
	}
	return plan
}

func (b *bench) requireNoParcel() {
	b.t.Helper()
	if entries, _ := os.ReadDir(b.stateDir.Name() + "/outgoing"); len(entries) != 0 {
		b.t.Fatalf("a parcel was left: %v", entries)
	}
}

// Un plan à deux machines virtuelles : pour chacune, produire, recevoir, puis
// élaguer, avant de passer à la suivante, avec un seul colis vivant à la fois.
// La destination reçoit l'archive chiffrée et la clé scellée côte à côte ; le
// serveur n'a vu aucun clair, et la clé de la phrase l'ouvre sans lui.
func TestRunNow_AnAgentPlanProducesReceivesAndPrunesOneTargetAfterTheOther(t *testing.T) {
	b := newBench(t, rights)
	destination := b.readyAgent("100,101", 5)
	keyring, err := b.db.GetBackupKeyring(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fleet := playAgents(t, b)

	first, err := b.backups.RunNow(operator(), "pve")
	if err != nil || first.Kind != agentwire.KindBackupProduce || first.MachineID != sourceID {
		t.Fatalf("first order %+v %v", first, err)
	}
	b.backups.Settle()

	want := "produce 100, receive 100, prune pve-vm-100, produce 101, receive 101, prune pve-vm-101"
	if got := fleet.summary(); got != want {
		t.Fatalf("steps\n got %s\nwant %s", got, want)
	}
	for index, step := range fleet.steps {
		wantMachine, wantLive := machineID, 1
		if strings.HasPrefix(step, "produce") {
			wantMachine = sourceID
		}
		if strings.HasPrefix(step, "prune") {
			// Le colis est jeté avant l'élagage : rien ne s'accumule.
			wantLive = 0
		}
		if fleet.machines[index] != wantMachine || fleet.live[index] != wantLive {
			t.Errorf("%s: machine %s, %d live parcels; want %s, %d", step, fleet.machines[index], fleet.live[index], wantMachine, wantLive)
		}
	}
	b.requireNoParcel()

	wantPrunes := []agentwire.BackupPruneParams{
		{Path: destination.Path, Prefix: "pve-vm-100", Keep: 5},
		{Path: destination.Path, Prefix: "pve-vm-101", Keep: 5},
	}
	if !slices.Equal(fleet.prunes, wantPrunes) {
		t.Fatalf("prunes %+v", fleet.prunes)
	}

	identity, err := backup.OpenIdentity(keyring.IdentitySealed, testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"100", "101"} {
		files := fleet.delivered[target]
		archive := files[agentwire.BackupFileName("pve-vm-"+target, benchNow)]
		sealedKey := files[agentwire.BackupKeyFileName(keyring.Fingerprint)]
		if len(files) != 2 || len(archive) == 0 || !bytes.Equal(sealedKey, keyring.IdentitySealed) {
			t.Fatalf("delivered for %s: %d files", target, len(files))
		}
		if bytes.Contains(archive, []byte(clearPrefix)) {
			t.Fatalf("the archive of %s is in the clear", target)
		}
		reader, err := age.Decrypt(bytes.NewReader(archive), identity)
		if err != nil {
			t.Fatal(err)
		}
		opened, _ := io.ReadAll(reader)
		if string(opened) != clearPrefix+target {
			t.Fatalf("restored %q", opened)
		}
	}

	plan := b.plan("pve")
	if plan.LastError != "" || plan.LastRunID == "" || !plan.LastAttemptAt.Equal(benchNow) {
		t.Fatalf("plan %+v", plan)
	}
	last, err := b.db.GetActionRun(context.Background(), plan.LastRunID)
	if err != nil || last.Kind != agentwire.KindBackupReceive || last.State != action.StateSucceeded {
		t.Fatalf("last run %+v %v", last, err)
	}
	overview, err := b.backups.Overview(context.Background())
	if err != nil || overview.Plans[1].LastTargetsOK != 2 || overview.Plans[1].LastTargetsFailed != 0 {
		t.Fatalf("overview %+v %v", overview.Plans, err)
	}
	// Le plan de la base n'a pas été touché.
	if database := b.plan(backup.SubjectDatabase); !database.LastAttemptAt.IsZero() {
		t.Fatalf("database %+v", database)
	}

	events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 20, ObjectKind: audit.ObjectBackupPlan})
	var started int
	for _, event := range events {
		if event.Action == audit.BackupPlanRun && event.ObjectName == "pve" && event.Details["targets"] == "2" {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("audit: %+v", events)
	}
}

// Une cible qui échoue n'arrête pas les suivantes ; le plan garde le premier
// souci avec la cible, les comptes disent combien ont réussi, et rien n'est
// élagué pour ce qui n'est pas arrivé.
func TestRunNow_AFailingTargetDoesNotStopTheNextOnes(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100,101,102", 3)
	fleet := playAgents(t, b)
	fleet.failProduce["100"] = agentwire.ReasonProducerFailed
	// Produite et téléversée, mais refusée par la destination : la cible est en
	// échec, son colis est jeté, rien n'est élagué, et la suivante part.
	fleet.refuseReceive["101"] = agentwire.ReasonDiskFull

	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()

	want := "produce 100, produce 101, receive 101, produce 102, receive 102, prune pve-vm-102"
	if got := fleet.summary(); got != want {
		t.Fatalf("steps\n got %s\nwant %s", got, want)
	}
	plan := b.plan("pve")
	if plan.LastError != "backup.target_failed:100:producer_failed" {
		t.Fatalf("last error %q", plan.LastError)
	}
	overview, _ := b.backups.Overview(context.Background())
	if pve := overview.Plans[1]; pve.LastTargetsOK != 1 || pve.LastTargetsFailed != 2 {
		t.Fatalf("counts %d ok, %d failed", pve.LastTargetsOK, pve.LastTargetsFailed)
	}
	b.requireNoParcel()
}

// Ce qu'un agent écrit dans un ordre refusé n'entre pas dans last_error : une
// raison de la liste fermée passe, tout le reste devient « unknown ».
func TestRunNow_OnlyAKnownReasonReachesTheLastError(t *testing.T) {
	cases := []struct {
		name   string
		state  agentwire.ActionState
		reason string
		want   string
	}{
		{"a failure of the producer", agentwire.ActionFailed, agentwire.ReasonProducerFailed, "backup.target_failed:100:producer_failed"},
		{"a refusal for a right", agentwire.ActionRefused, agentwire.RightMissing(machine.CapabilityBackupProduce), "backup.target_failed:100:right_missing:backup.produce"},
		{"a right that is not one", agentwire.ActionRefused, "right_missing:rm -rf", "backup.target_failed:100:unknown"},
		{"a sentence of the agent", agentwire.ActionFailed, "vzdump exploded with swordfish", "backup.target_failed:100:unknown"},
		{"a reason carrying a separator", agentwire.ActionFailed, "disk_full:101:x", "backup.target_failed:100:unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBench(t, rights)
			b.readyAgent("100", 3)
			fleet := playAgents(t, b)
			if c.state == agentwire.ActionRefused {
				fleet.refuseProduce["100"] = c.reason
			} else {
				fleet.failProduce["100"] = c.reason
			}
			if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
				t.Fatal(err)
			}
			b.backups.Settle()
			plan := b.plan("pve")
			if plan.LastError != c.want || strings.Contains(plan.LastError, "swordfish") {
				t.Fatalf("last error %q, want %q", plan.LastError, c.want)
			}
			// Le composant des ordres journalise ce qu'il reçoit ; le composant
			// des sauvegardes, lui, ne répète jamais la phrase de l'agent.
			for _, line := range strings.Split(b.logs.String(), "\n") {
				if strings.Contains(line, `msg="backup `) && strings.Contains(line, "swordfish") {
					t.Fatalf("the agent's sentence reached the backup log: %s", line)
				}
			}
		})
	}
}

// Un agent qui dit avoir produit sans rien téléverser n'a rien produit : la
// cible échoue, la destination ne reçoit rien.
func TestRunNow_AProducerThatUploadedNothingFailsTheTarget(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100", 3)
	fleet := playAgents(t, b)
	fleet.noArchive["100"] = true

	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()
	if got := fleet.summary(); got != "produce 100" {
		t.Fatalf("steps %s", got)
	}
	if plan := b.plan("pve"); plan.LastError != "backup.target_failed:100:upload_failed" {
		t.Fatalf("last error %q", plan.LastError)
	}
	b.requireNoParcel()
}

// Une source compromise téléverse, à côté de son archive, un nom qui n'est
// pas celui de sa cible : une fausse sauvegarde de la base datée de 2099, ou
// l'archive d'une autre machine virtuelle. La destination l'écrirait, et
// l'élagage de ce préfixe chasserait les vraies sauvegardes : rien ne part,
// la cible est en échec, la suivante est jugée à son tour.
func TestRunNow_AParcelWithAnotherNameIsNotDelivered(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100,101", 3)
	fleet := playAgents(t, b)
	fleet.extraUpload["100"] = "opencloud-20991231-235959.tar.age"
	fleet.extraUpload["101"] = agentwire.BackupFileName("pve-vm-102", benchNow)

	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()
	if got := fleet.summary(); got != "produce 100, produce 101" {
		t.Fatalf("steps %s", got)
	}
	if len(fleet.delivered) != 0 || len(fleet.prunes) != 0 {
		t.Fatalf("delivered %v, prunes %v", fleet.delivered, fleet.prunes)
	}
	if plan := b.plan("pve"); plan.LastError != "backup.target_failed:100:upload_failed" {
		t.Fatalf("last error %q", plan.LastError)
	}
	b.requireNoParcel()
}

// Un ordre que l'agent a accepté et jamais fini échoue au bout de
// MaxRunDuration : la cible est notée en timeout, la suivante part.
func TestRunNow_ATargetThatNeverEndsTimesOutAndTheNextOneRuns(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100,101", 3)
	fleet := playAgents(t, b)
	fleet.stuck["100"] = true
	fleet.afterStuck = func(string) {
		// Le temps passe, la boucle de fond du serveur fait son tour.
		b.now = b.now.Add(action.MaxRunDuration + 2*time.Minute)
		if err := b.actions.Timeout(context.Background()); err != nil {
			t.Error(err)
		}
	}

	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()
	want := "produce 100, produce 101, receive 101, prune pve-vm-101"
	if got := fleet.summary(); got != want {
		t.Fatalf("steps\n got %s\nwant %s", got, want)
	}
	if plan := b.plan("pve"); plan.LastError != "backup.target_failed:100:timeout" {
		t.Fatalf("last error %q", plan.LastError)
	}
	b.requireNoParcel()
}

// « all » lit les invités que l'hôte Proxmox de la source a relevés ; sans
// lecture, le plan demande la liste des numéros.
func TestRunNow_AllNeedsTheGuestsOfTheSource(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent(backup.SelectorAll, 3)
	fleet := playAgents(t, b)

	_, err := b.backups.RunNow(operator(), "pve")
	requireRefusal(t, err, backup.ErrSelectorNeedsList)
	b.backups.SetGuests(fakeGuests{sourceID: nil})
	_, err = b.backups.RunNow(operator(), "pve")
	requireRefusal(t, err, backup.ErrSelectorNeedsList)
	if plan := b.plan("pve"); plan.LastError != "backup.selector_needs_list" || len(fleet.steps) != 0 {
		t.Fatalf("plan %+v, steps %v", plan, fleet.steps)
	}
	b.requireNoParcel()

	b.backups.SetGuests(fakeGuests{sourceID: {100, 200}})
	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()
	want := "produce 100, receive 100, prune pve-vm-100, produce 200, receive 200, prune pve-vm-200"
	if got := fleet.summary(); got != want {
		t.Fatalf("steps\n got %s\nwant %s", got, want)
	}
	if plan := b.plan("pve"); plan.LastError != "" {
		t.Fatalf("plan %+v", plan)
	}
}

// Ce que le serveur juge avant le moindre ordre : rien ne part, aucun colis
// n'est créé, le plan garde la raison, et le verrou est rendu.
func TestRunNow_RefusesBeforeAnyOrderWhenTheTwoMachinesAreNotReady(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, b *bench)
		want  error
	}{
		{"a source that is offline", func(t *testing.T, b *bench) {
			source, _ := b.machines.Get(context.Background(), sourceID)
			if source.Online {
				t.Fatal("the source must start online")
			}
		}, backup.ErrSourceOffline},
		{"a source whose agent may not produce", func(t *testing.T, b *bench) {
			b.connectSource("backup.receive=/srv/autre")
		}, backup.ErrSourceRightMissing},
		{"a destination that is offline", func(t *testing.T, b *bench) {
			b.connectSource(sourceRights)
			b.machines.Disconnect(b.session)
		}, backup.ErrMachineOffline},
		{"a destination without the right", func(t *testing.T, b *bench) {
			b.connectSource(sourceRights)
			b.reconnectDestination(currentAgentVersion, "backup.receive=/srv/autre")
		}, backup.ErrRightMissing},
		{"a destination agent that cannot prune by prefix", func(t *testing.T, b *bench) {
			b.connectSource(sourceRights)
			b.reconnectDestination("v0.1.9", rights)
		}, backup.ErrDestinationAgentOutdated},
		{"a destination agent of the version deployed before the prefix", func(t *testing.T, b *bench) {
			b.connectSource(sourceRights)
			b.reconnectDestination("v0.1.0", rights)
		}, backup.ErrDestinationAgentOutdated},
		{"a destination agent in development", func(t *testing.T, b *bench) {
			b.connectSource(sourceRights)
			b.reconnectDestination("dev", rights)
		}, backup.ErrDestinationAgentOutdated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBench(t, rights)
			_, destination := b.ready()
			b.withSource()
			if _, err := b.backups.SavePlan(operator(), agentPlanFor(destination.ID, "100", 3)); err != nil {
				t.Fatal(err)
			}
			c.setup(t, b)
			fleet := playAgents(t, b)

			_, err := b.backups.RunNow(operator(), "pve")
			requireRefusal(t, err, c.want)
			key, _ := refusal.KeyOf(err)
			if plan := b.plan("pve"); plan.LastError != key || plan.LastRunID != "" {
				t.Fatalf("plan %+v, want %s", plan, key)
			}
			if len(b.commands.orders) != 0 || len(fleet.steps) != 0 {
				t.Fatalf("orders were sent: %v", b.commands.kinds)
			}
			b.requireNoParcel()
			// Le verrou est rendu : le plan de la base part.
			if c.want != backup.ErrMachineOffline && c.want != backup.ErrRightMissing {
				if _, err := b.backups.RunNow(operator(), backup.SubjectDatabase); err != nil {
					t.Fatalf("database after a refusal: %v", err)
				}
			}
		})
	}
}

// Sans clé, une source agent ne part pas non plus.
func TestRunNow_AnAgentPlanNeedsTheKeyring(t *testing.T) {
	b := newBench(t, rights)
	b.connectSource(sourceRights)
	destination, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "store-1", MachineID: machineID, Path: "/srv/backups"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.backups.SavePlan(operator(), agentPlanFor(destination.ID, "100", 3)); err != nil {
		t.Fatal(err)
	}
	_, err = b.backups.RunNow(operator(), "pve")
	requireRefusal(t, err, backup.ErrKeyringMissing)
	if plan := b.plan("pve"); plan.LastError != "backup.keyring_missing" || len(b.commands.orders) != 0 {
		t.Fatalf("plan %+v", plan)
	}
}

// Le garde-fou ne concerne que les plans agent : la base n'a pas de préfixe à
// confondre, et son plan continue de partir vers un agent plus ancien.
func TestRunNow_TheDatabasePlanStillGoesToAnOlderAgent(t *testing.T) {
	b := newBench(t, rights)
	b.ready()
	for _, version := range []string{"v0.0.1", "v0.1.0", "dev"} {
		b.reconnectDestination(version, rights)
		if _, err := b.backups.RunNow(operator(), backup.SubjectDatabase); err != nil {
			t.Fatalf("database to an agent %s: %v", version, err)
		}
	}
}

// Le garde-fou compare des versions, pas des textes : v0.10.0 est plus récente
// que v0.2.0, et ce qui n'est pas une version est trop ancien.
func TestCheckDestinationVersion(t *testing.T) {
	for version, ok := range map[string]bool{
		"v0.2.0": true, "v0.2.1": true, "v0.10.0": true, "v1.0.0": true,
		"v0.1.9": false, "v0.1.0": false, "v0.0.1": false, "dev": false, "": false, "0.2": false,
	} {
		err := backup.CheckDestinationVersion(version)
		if ok && err != nil || !ok && !errors.Is(err, backup.ErrDestinationAgentOutdated) {
			t.Errorf("%q: %v, want ok=%v", version, err, ok)
		}
	}
	if agentwire.PrefixSinceVersion != "v0.2.0" {
		t.Errorf("the guard moved to %s: the documentation says v0.2.0", agentwire.PrefixSinceVersion)
	}
}

// L'agent de la destination a pu être remplacé par un plus ancien pendant le
// passage : la sauvegarde arrivée reste, l'élagage ne part pas, et le plan le
// dit avec le refus du garde-fou.
func TestRunNow_ADestinationReplacedByAnOlderAgentIsNotPruned(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100,101", 3)
	fleet := playAgents(t, b)
	fleet.beforeReceive = func(target string) {
		if target == "100" {
			b.reconnectDestination("v0.1.0", rights)
		}
	}

	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()
	if got := fleet.summary(); got != "produce 100, receive 100, produce 101, receive 101" {
		t.Fatalf("steps %s", got)
	}
	plan := b.plan("pve")
	if plan.LastError != "backup.destination_agent_outdated" {
		t.Fatalf("last error %q", plan.LastError)
	}
	overview, _ := b.backups.Overview(context.Background())
	if pve := overview.Plans[1]; pve.LastTargetsOK != 2 || pve.LastTargetsFailed != 0 {
		t.Fatalf("counts %d ok, %d failed", pve.LastTargetsOK, pve.LastTargetsFailed)
	}
}

// Un élagage qui échoue laisse la destination grossir : la sauvegarde est
// reçue (comptée), le plan dit quelle cible n'a pas été élaguée.
func TestRunNow_APruneThatFailsIsNotedAndTheNextTargetRuns(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100,101", 3)
	fleet := playAgents(t, b)
	fleet.failPrune["pve-vm-100"] = agentwire.ReasonPathUnwritable

	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()
	want := "produce 100, receive 100, prune pve-vm-100, produce 101, receive 101, prune pve-vm-101"
	if got := fleet.summary(); got != want {
		t.Fatalf("steps\n got %s\nwant %s", got, want)
	}
	if plan := b.plan("pve"); plan.LastError != "backup.prune_failed:100:path_unwritable" {
		t.Fatalf("last error %q", plan.LastError)
	}
	overview, _ := b.backups.Overview(context.Background())
	if pve := overview.Plans[1]; pve.LastTargetsOK != 2 || pve.LastTargetsFailed != 0 {
		t.Fatalf("counts %d ok, %d failed", pve.LastTargetsOK, pve.LastTargetsFailed)
	}
}

// Un passage dure des heures : tant qu'il court, un second ne part pas, sur ce
// plan comme sur un autre ; quand il finit, le verrou est rendu.
func TestRunNow_RefusesASecondPassWhileOneRuns(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100", 3)
	fleet := playAgents(t, b)
	fleet.stuck["100"] = true

	first, err := b.backups.RunNow(operator(), "pve")
	if err != nil || first.Kind != agentwire.KindBackupProduce {
		t.Fatalf("first %+v %v", first, err)
	}
	for _, subject := range []string{"pve", backup.SubjectDatabase} {
		_, err = b.backups.RunNow(operator(), subject)
		requireRefusal(t, err, backup.ErrRunInProgress)
		if !refusal.IsConflict(err) {
			t.Fatal("a pass in progress is a conflict")
		}
	}
	if plan := b.plan("pve"); plan.LastError != "" || plan.LastRunID != first.ID {
		t.Fatalf("the refusal touched the plan: %+v", plan)
	}
	if got := b.countOrders(agentwire.KindBackupProduce); got != 1 {
		t.Fatalf("%d produce orders", got)
	}

	fleet.release(sourceID, "100")
	b.backups.Settle()
	if got := fleet.summary(); got != "produce 100, receive 100, prune pve-vm-100" {
		t.Fatalf("steps %s", got)
	}
	if _, err := b.backups.RunNow(operator(), backup.SubjectDatabase); err != nil {
		t.Fatalf("after the pass: %v", err)
	}
}

// panickingGuests fait paniquer la préparation d'un passage.
type panickingGuests struct{}

func (panickingGuests) ListProxmoxGuestIDs(context.Context, string) ([]int, error) {
	panic("relevé illisible")
}

// Une panique pendant la préparation remonte au serveur, qui la rattrape et
// continue : le verrou des passages doit être rendu, sinon plus aucune
// sauvegarde ne part avant un redémarrage.
func TestRunNow_APanicBeforeThePassReleasesTheLock(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent(backup.SelectorAll, 3)
	b.backups.SetGuests(panickingGuests{})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic did not reach the caller")
			}
		}()
		_, _ = b.backups.RunNow(operator(), "pve")
	}()
	if _, err := b.backups.RunNow(operator(), backup.SubjectDatabase); err != nil {
		t.Fatalf("after a panic: %v", err)
	}
}

// Le serveur qui s'arrête ne note pas d'échec : le colis en cours est jeté, le
// plan garde ce qu'il avait.
func TestRunNow_AServerShutdownInterruptsThePassWithoutNotingAFailure(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100,101", 3)
	lifetime, stop := context.WithCancel(context.Background())
	defer stop()
	b.backups.SetLifetime(lifetime)
	fleet := playAgents(t, b)
	fleet.stuck["100"] = true

	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	stop()
	b.backups.Settle()
	if got := fleet.summary(); got != "produce 100" {
		t.Fatalf("steps %s", got)
	}
	if plan := b.plan("pve"); plan.LastError != "" {
		t.Fatalf("last error %q", plan.LastError)
	}
	b.requireNoParcel()
}

// La nuit mène le passage d'un plan agent jusqu'au bout, sous l'acteur
// « backup », et une seule fois.
func TestRunIfDue_RunsAnAgentPlanToTheEnd(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100,101", 3)
	if err := b.backups.DeletePlan(operator(), backup.SubjectDatabase); err != nil {
		t.Fatal(err)
	}
	fleet := playAgents(t, b)

	b.backups.RunIfDue(context.Background())
	want := "produce 100, receive 100, prune pve-vm-100, produce 101, receive 101, prune pve-vm-101"
	if got := fleet.summary(); got != want {
		t.Fatalf("steps\n got %s\nwant %s", got, want)
	}
	plan := b.plan("pve")
	if plan.LastError != "" || !plan.LastAttemptAt.Equal(benchNow) {
		t.Fatalf("plan %+v", plan)
	}
	runs, err := b.db.ListActionRuns(context.Background(), action.Query{MachineID: sourceID, Limit: 10})
	if err != nil || len(runs) != 2 || runs[0].RequestedBy != "rule:backup" {
		t.Fatalf("runs %+v %v", runs, err)
	}
	b.requireNoParcel()

	b.backups.RunIfDue(context.Background())
	if got := len(fleet.steps); got != 6 {
		t.Fatalf("a second pass the same night: %d steps", got)
	}
}

// La nuit, un passage dont la première cible ne part pas (le flux de la source
// tombe le temps de l'envoi) commence quand même : il s'audite, la cible est
// notée en échec, et la suivante part.
func TestRunIfDue_AuditsAPassWhoseFirstTargetCannotLeave(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100,101", 3)
	if err := b.backups.DeletePlan(operator(), backup.SubjectDatabase); err != nil {
		t.Fatal(err)
	}
	fleet := playAgents(t, b)
	b.commands.refuseOnce = errors.New("stream closed")

	b.backups.RunIfDue(context.Background())
	if got := fleet.summary(); got != "produce 101, receive 101, prune pve-vm-101" {
		t.Fatalf("steps %s", got)
	}
	if plan := b.plan("pve"); plan.LastError != "backup.target_failed:100:undelivered" || !plan.LastAttemptAt.Equal(benchNow) {
		t.Fatalf("plan %+v", plan)
	}
	events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 20, ObjectKind: audit.ObjectBackupPlan})
	started := 0
	for _, event := range events {
		if event.Action == audit.BackupPlanRun && event.ObjectName == "pve" {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("%d pass starts audited", started)
	}
}

func (b *bench) countOrders(kind agentwire.Kind) int {
	count := 0
	for _, sent := range b.commands.kinds {
		if sent == kind {
			count++
		}
	}
	return count
}
