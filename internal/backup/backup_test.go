package backup_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

const (
	machineID = "11111111-2222-4333-8444-555555555555"
	// Une phrase de test, trop longue pour être un mot de passe réel.
	testPassphrase = "une phrase de test assez longue" // #nosec G101 // gitleaks:allow
	// Le coût de scrypt des tests : le défaut d'age demande un quart de gigaoctet.
	testWorkFactor = 10
	rights         = "backup.receive=/srv/backups"
	// L'agent de la destination connaît le préfixe de backup.prune
	// (agentwire.PrefixSinceVersion).
	currentAgentVersion = "v0.2.0"
)

var benchNow = time.Date(2026, 10, 21, 3, 20, 0, 0, time.UTC)

// commander tient lieu du flux de l'agent : il garde les ordres signés. onOrder
// joue l'agent, quand un test en veut un : il voit l'ordre au moment où il
// part, avec la machine qui le reçoit, et le termine par Record.
type commander struct {
	refuse  error
	orders  []agentwire.OrderBody
	kinds   []agentwire.Kind
	onOrder func(machineID string, order agentwire.OrderBody)
}

func (c *commander) Command(machineID string, command agentwire.Command) error {
	if c.refuse != nil {
		return c.refuse
	}
	var body agentwire.OrderBody
	if err := json.Unmarshal(command.Order.Body, &body); err != nil {
		return err
	}
	c.orders = append(c.orders, body)
	c.kinds = append(c.kinds, command.Kind)
	if c.onOrder != nil {
		c.onOrder(machineID, body)
	}
	return nil
}

type bench struct {
	t        *testing.T
	db       *store.DB
	stateDir *os.Root
	machines *machine.Service
	actions  *action.Service
	outbox   *action.Outbox
	backups  *backup.Service
	commands *commander
	logs     *bytes.Buffer
	now      time.Time
	session  *machine.Session
}

func newBench(t *testing.T, reportedRights string) *bench {
	t.Helper()
	stateDir, db := storetest.OpenWithStateDir(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b := &bench{t: t, db: db, stateDir: stateDir, now: benchNow, commands: &commander{}, logs: &logs}
	clock := func() time.Time { return b.now }

	b.machines = machine.New(db, machine.NewSessions(), logger)
	b.machines.SetClock(clock)
	enrolled := storetest.EnrollMachine(t, b.machines, machineID, "store-1")
	var err error
	b.session, err = b.machines.Connect(context.Background(), enrolled, "192.0.2.1", currentAgentVersion, reportedRights, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.machines.Disconnect(b.session) })

	journal := audit.New(db, logger)
	b.actions = action.New(db, b.machines, logger)
	b.actions.SetClock(clock)
	b.actions.SetAuditor(journal)
	b.actions.SetKey(action.GenerateKey())
	b.actions.SetCommander(b.commands)
	b.outbox, err = action.NewOutbox(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	b.outbox.SetClock(clock)
	b.actions.SetOutbox(b.outbox)

	// Les deux clés que la sauvegarde emporte avec la base.
	if err := stateDir.WriteFile(brand.SecretKeyFile, []byte("clé de chiffrement de test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stateDir.WriteFile(brand.ServerKeyFile, []byte("ed25519-v1:clé de test"), 0o600); err != nil {
		t.Fatal(err)
	}

	b.backups = backup.New(db, b.machines, stateDir, logger)
	b.backups.SetClock(clock)
	b.backups.SetWorkFactor(testWorkFactor)
	b.backups.SetAuditor(journal)
	b.backups.SetSender(b.actions)
	b.backups.SetParcels(b.outbox)
	return b
}

func operator() context.Context {
	return audit.WithActor(context.Background(), audit.Operator("lucas"))
}

// ready pose ce qu'il faut pour qu'une sauvegarde parte : la clé, une
// destination, un plan.
func (b *bench) ready() (backup.Keyring, backup.Destination) {
	b.t.Helper()
	keyring, err := b.backups.CreateKeyring(operator(), testPassphrase, false)
	if err != nil {
		b.t.Fatal(err)
	}
	destination, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "store-1", MachineID: machineID, Path: "/srv/backups"})
	if err != nil {
		b.t.Fatal(err)
	}
	if _, err := b.backups.SavePlan(operator(), databasePlan(destination.ID, 3)); err != nil {
		b.t.Fatal(err)
	}
	return keyring, destination
}

// databasePlan est le plan de la base vers cette destination.
func databasePlan(destinationID string, keep int) backup.PlanDefinition {
	return backup.PlanDefinition{Subject: backup.SubjectDatabase, SourceKind: backup.SourceDatabase, DestinationID: destinationID, Keep: keep}
}

func requireRefusal(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

// La clé privée n'est ouverte que par la phrase, et la phrase ne se trouve
// nulle part : ni dans la base (fichier et journal WAL compris), ni dans
// l'audit, ni dans le journal du serveur.
func TestCreateKeyring_SealsAnIdentityThatOnlyThePassphraseOpens(t *testing.T) {
	b := newBench(t, rights)
	keyring, err := b.backups.CreateKeyring(operator(), "  "+testPassphrase+" ", false)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := backup.OpenIdentity(keyring.IdentitySealed, testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Recipient().String() != keyring.Recipient || len(keyring.Fingerprint) != 16 {
		t.Fatalf("keyring %+v", keyring)
	}
	if _, err := backup.OpenIdentity(keyring.IdentitySealed, "une autre phrase de test longue"); !errors.Is(err, backup.ErrWrongPassphrase) {
		t.Fatalf("a wrong passphrase: %v", err)
	}

	stored, err := b.db.GetBackupKeyring(context.Background())
	if err != nil || stored.Recipient != keyring.Recipient || !bytes.Equal(stored.IdentitySealed, keyring.IdentitySealed) {
		t.Fatalf("stored %+v %v", stored, err)
	}
	if bytes.Contains(stored.IdentitySealed, []byte("AGE-SECRET-KEY-")) {
		t.Fatal("the private key is stored in the clear")
	}
	for _, name := range []string{brand.DatabaseFile, brand.DatabaseFile + "-wal"} {
		content, err := b.stateDir.ReadFile(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(content, []byte(testPassphrase)) {
			t.Errorf("the passphrase is in %s", name)
		}
	}
	events, err := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectBackupKeyring})
	if err != nil || len(events) != 1 || events[0].Action != audit.BackupKeyringCreate || events[0].Details["fingerprint"] != keyring.Fingerprint {
		t.Fatalf("events %+v %v", events, err)
	}
	for _, detail := range events[0].Details {
		if strings.Contains(detail, "phrase") {
			t.Errorf("the passphrase reached the audit: %v", events[0].Details)
		}
	}
	if strings.Contains(b.logs.String(), "phrase de test") {
		t.Errorf("the passphrase reached the log: %s", b.logs.String())
	}
}

func TestCreateKeyring_RefusesAShortPassphraseAndASecondWithoutReplace(t *testing.T) {
	b := newBench(t, rights)
	for _, short := range []string{"", "courte", "  quinze caractèr  ", strings.Repeat("é", backup.MinPassphrase-1)} {
		_, err := b.backups.CreateKeyring(operator(), short, false)
		requireRefusal(t, err, backup.ErrPassphraseTooShort)
	}
	// Seize caractères, et non seize octets : « é » en prend deux.
	if _, err := b.backups.CreateKeyring(operator(), strings.Repeat("é", backup.MinPassphrase), false); err != nil {
		t.Fatalf("16 runes: %v", err)
	}
	if _, err := b.backups.CreateKeyring(operator(), strings.Repeat("x", backup.MaxPassphrase+1), true); !errors.Is(err, backup.ErrPassphraseTooShort) {
		t.Fatalf("too long: %v", err)
	}
	_, err := b.backups.CreateKeyring(operator(), testPassphrase, false)
	requireRefusal(t, err, backup.ErrKeyringExists)
	if !refusal.IsConflict(err) {
		t.Fatal("an existing key is a conflict")
	}
	first, _ := b.db.GetBackupKeyring(context.Background())
	replaced, err := b.backups.CreateKeyring(operator(), testPassphrase, true)
	if err != nil || replaced.Fingerprint == first.Fingerprint {
		t.Fatalf("replace: %+v %v", replaced, err)
	}
	events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectBackupKeyring})
	if len(events) != 2 || events[0].Action != audit.BackupKeyringReplace {
		t.Fatalf("events %+v", events)
	}
}

func TestCreateDestination_RefusesAPathOutsideTheReportedRight(t *testing.T) {
	b := newBench(t, rights)
	for path, want := range map[string]error{
		"/srv/backups2":     backup.ErrRightMissing,
		"/srv":              backup.ErrRightMissing,
		"/srv/backups/../x": backup.ErrPathInvalid,
		"srv/backups":       backup.ErrPathInvalid,
		"/":                 backup.ErrPathInvalid,
	} {
		_, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "store-1", MachineID: machineID, Path: path})
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", path, err, want)
		}
	}
	_, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "Store 1", MachineID: machineID, Path: "/srv/backups"})
	requireRefusal(t, err, backup.ErrNameInvalid)
	_, err = b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "store-1", MachineID: "inconnue", Path: "/srv/backups"})
	requireRefusal(t, err, backup.ErrMachineUnknown)

	if _, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "store-1", MachineID: machineID, Path: "/srv/backups/opencloud"}); err != nil {
		t.Fatalf("a subfolder of the right: %v", err)
	}
	_, err = b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "store-1", MachineID: machineID, Path: "/srv/backups"})
	requireRefusal(t, err, backup.ErrNameTaken)
}

// Tant que l'agent n'a rien rapporté de ses droits, c'est lui qui jugera à
// l'exécution : la destination se crée, la page dit « pas encore rapportés ».
func TestCreateDestination_AcceptsWhileRightsAreUnreported(t *testing.T) {
	b := newBench(t, "")
	destination, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "store-1", MachineID: machineID, Path: "/srv/anywhere"})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := b.backups.Overview(context.Background())
	if err != nil || len(overview.Destinations) != 1 || overview.Destinations[0].ID != destination.ID || overview.Destinations[0].Right != backup.RightUnknown {
		t.Fatalf("overview %+v %v", overview, err)
	}
	if overview.Keyring != nil || len(overview.Plans) != 0 {
		t.Fatalf("nothing else exists yet: %+v", overview)
	}
}

func TestCreateDestination_StopsAtTheCeiling(t *testing.T) {
	b := newBench(t, rights)
	for index := range backup.MaxDestinations {
		name := "dest-" + string(rune('a'+index))
		if _, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: name, MachineID: machineID, Path: "/srv/backups"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	_, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "one-more", MachineID: machineID, Path: "/srv/backups"})
	requireRefusal(t, err, backup.ErrTooManyDestinations)
}

// Une sauvegarde : un colis dont l'archive se déchiffre avec la phrase en
// trois fichiers dont une base saine, la clé scellée à côté, aucune copie en
// clair ; puis deux ordres, la réception puis l'élagage.
func TestRun_StagesAnEncryptedArchiveAndSendsReceiveThenPrune(t *testing.T) {
	b := newBench(t, rights)
	keyring, destination := b.ready()

	run, err := b.backups.RunNow(operator(), backup.SubjectDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if run.Kind != agentwire.KindBackupReceive || run.State != action.StateSent {
		t.Fatalf("run %+v", run)
	}
	if len(b.commands.orders) != 2 || b.commands.kinds[0] != agentwire.KindBackupReceive || b.commands.kinds[1] != agentwire.KindBackupPrune {
		t.Fatalf("orders %+v", b.commands.kinds)
	}

	var receive agentwire.BackupReceiveParams
	if err := json.Unmarshal(b.commands.orders[0].Params, &receive); err != nil {
		t.Fatal(err)
	}
	archiveName := agentwire.BackupFileName(backup.SubjectDatabase, benchNow)
	keyName := agentwire.BackupKeyFileName(keyring.Fingerprint)
	if receive.Path != "/srv/backups" || len(receive.Files) != 2 || receive.Files[0].Name != archiveName || receive.Files[1].Name != keyName {
		t.Fatalf("receive %+v", receive)
	}
	var prune agentwire.BackupPruneParams
	if err := json.Unmarshal(b.commands.orders[1].Params, &prune); err != nil || prune.Path != "/srv/backups" || prune.Prefix != "opencloud" || prune.Keep != 3 {
		t.Fatalf("prune %+v %v", prune, err)
	}

	// Ce que l'agent télécharge : taille et somme sont celles de l'ordre.
	archive := downloaded(t, b.outbox, receive, archiveName)
	sealed := downloaded(t, b.outbox, receive, keyName)
	if !bytes.Equal(sealed, keyring.IdentitySealed) {
		t.Fatal("the sealed key beside the backup is not the stored one")
	}
	identity, err := backup.OpenIdentity(sealed, testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := age.Decrypt(bytes.NewReader(archive), identity)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	var order []string
	unpacker := tar.NewReader(plain)
	for {
		header, err := unpacker.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Mode != 0o600 {
			t.Errorf("%s mode %o", header.Name, header.Mode)
		}
		entries[header.Name], _ = io.ReadAll(unpacker)
		order = append(order, header.Name)
	}
	if strings.Join(order, ",") != brand.DatabaseFile+","+brand.SecretKeyFile+","+brand.ServerKeyFile {
		t.Fatalf("entries %v", order)
	}
	if string(entries[brand.SecretKeyFile]) != "clé de chiffrement de test" {
		t.Errorf("secret key entry %q", entries[brand.SecretKeyFile])
	}
	checkDatabase(t, entries[brand.DatabaseFile])

	// Rien en clair ne reste dans le colis, ni la base ni ses clés.
	parcelDir := "outgoing/" + receive.Parcel
	left, err := os.ReadDir(b.stateDir.Name() + "/" + parcelDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range left {
		if entry.Name() == "snapshot.db" {
			t.Fatal("the clear snapshot is still in the parcel")
		}
	}
	if len(left) != 2 {
		t.Fatalf("parcel holds %d files", len(left))
	}

	// Le plan se souvient de la tentative et de l'ordre.
	plan, err := b.db.GetBackupPlan(context.Background(), backup.SubjectDatabase)
	if err != nil || plan.LastRunID != run.ID || plan.LastError != "" || !plan.LastAttemptAt.Equal(benchNow) {
		t.Fatalf("plan %+v %v", plan, err)
	}
	events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 20, ObjectKind: audit.ObjectBackupPlan})
	if len(events) == 0 || events[0].Action != audit.BackupPlanRun || events[0].Details["run_id"] != run.ID || events[0].Details["destination_id"] != destination.ID {
		t.Fatalf("events %+v", events)
	}
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func downloaded(t *testing.T, outbox *action.Outbox, receive agentwire.BackupReceiveParams, name string) []byte {
	t.Helper()
	file, size, err := outbox.Open(machineID, receive.Parcel, name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil || int64(len(content)) != size {
		t.Fatalf("read %s: %v", name, err)
	}
	for _, listed := range receive.Files {
		if listed.Name == name && (listed.Size != size || listed.SHA256 != sha256Hex(content)) {
			t.Fatalf("%s: the order says %d bytes %s", name, listed.Size, listed.SHA256)
		}
	}
	return content
}

// checkDatabase ouvre la base de l'archive et la fait relire par SQLite.
func checkDatabase(t *testing.T, content []byte) {
	t.Helper()
	dir, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := dir.WriteFile(brand.DatabaseFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	copied, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("the archived database does not open: %v", err)
	}
	defer copied.Close()
	machines, err := copied.ListMachines(context.Background())
	if err != nil || len(machines) != 1 || machines[0].ID != machineID {
		t.Fatalf("the archived database lost its machines: %v %v", machines, err)
	}
}
