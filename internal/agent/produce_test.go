package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

const (
	producedPrefix = "pve-vm-5012"
	producedEntry  = "vzdump-qemu-5012-2026_10_21-12_00_00.vma.zst"
)

// producedName est le nom de l'archive que l'ordre de test produit : l'horloge
// du banc est figée.
var producedName = agentwire.BackupFileName(producedPrefix, orderNow)

// echoProducer écrit des octets connus et rend une entrée de tar, comme le fait
// un producteur sans lancer vzdump. exit, s'il y en a un, est son échec.
func echoProducer(content []byte, stderr []string, exit error) producer {
	return producer{
		validTarget: vzdumpTarget.MatchString,
		run: func(_ context.Context, request producerRequest) (producerResult, error) {
			result := producerResult{entry: producedEntry}
			for _, line := range stderr {
				result.log = append(result.log, agentwire.OutputLine{Stream: "stderr", Line: line})
			}
			if _, err := request.raw.Write(content); err != nil {
				return result, err
			}
			return result, exit
		},
	}
}

// testIdentity est la clé privée du trousseau de test : l'agent n'a que la
// publique, le test déchiffre avec la privée.
func testIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// openArchive déchiffre l'archive produite et rend l'unique entrée du tar, son
// nom et son contenu ; plus d'une entrée est une erreur.
func openArchive(t *testing.T, archive []byte, identity age.Identity) (string, []byte) {
	t.Helper()
	decrypted, err := age.Decrypt(bytes.NewReader(archive), identity)
	if err != nil {
		t.Fatalf("the archive does not decrypt with the private key: %v", err)
	}
	reader := tar.NewReader(decrypted)
	header, err := reader.Next()
	if err != nil {
		t.Fatalf("no entry: %v", err)
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("the tar holds more than one entry: %v", err)
	}
	if _, err := io.Copy(io.Discard, decrypted); err != nil {
		t.Fatalf("the age stream is not authentic to its end: %v", err)
	}
	return header.Name, content
}

func sumHex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func produceParams(identity *age.X25519Identity) agentwire.BackupProduceParams {
	return agentwire.BackupProduceParams{
		Producer: machine.ProducerProxmoxVzdump, Target: "5012", Prefix: producedPrefix,
		Recipient: identity.Recipient().String(), Parcel: "colis1",
	}
}

// produceRig est un agent qui a le droit backup.produce, dont le worker
// tourne, sur un faux serveur de colis. Le producteur est remplacé par chaque
// test (useProducer) et le compte du test peut tout lancer.
type produceRig struct {
	*orderBench
	server   *parcelServer
	identity *age.X25519Identity
	produced []string
}

func newProduceRig(t *testing.T) *produceRig {
	t.Helper()
	b := newOrderBenchWithRights(t, []string{"observe", "backup.produce=" + machine.ProducerProxmoxVzdump})
	server := &parcelServer{files: map[string]string{}}
	b.runner.fetch.use(server.client(t))
	b.runner.fetch.setSession(parcelSession)
	b.runner.produce.freeBytes = func(string) (uint64, error) { return 100 << 30, nil }
	b.runner.produce.retryDelay = 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.runner.worker.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &produceRig{orderBench: b, server: server, identity: testIdentity(t)}
}

func (b *produceRig) useProducer(chosen producer) {
	b.runner.produce.producers[machine.ProducerProxmoxVzdump] = chosen
}

// countingProducer garde le nombre de lancements : un refus ne lance rien.
func (b *produceRig) countingProducer(content []byte) {
	inner := echoProducer(content, nil, nil)
	b.useProducer(producer{validTarget: inner.validTarget, run: func(ctx context.Context, request producerRequest) (producerResult, error) {
		b.produced = append(b.produced, request.target)
		return inner.run(ctx, request)
	}})
}

func (b *produceRig) produceDir() string {
	return filepath.Join(b.stateDir.Name(), "produce")
}

// run envoie l'ordre et rend ses résultats : « accepté », puis la fin.
func (b *produceRig) run(t *testing.T, seq int64, params agentwire.BackupProduceParams) []agentwire.ActionResult {
	t.Helper()
	body := longOrder(seq, agentwire.KindBackupProduce, params)
	b.runner.receive(b.signed(t, body))
	return b.until(t, body.RunID)
}

func lines(output []agentwire.OutputLine) []string {
	out := make([]string, 0, len(output))
	for _, line := range output {
		out = append(out, line.Line)
	}
	return out
}

// Le chemin entier : le producteur écrit ses octets, l'agent les emballe dans
// un tar d'une entrée, les chiffre pour la clé publique, vérifie le fichier et
// le téléverse. L'archive se déchiffre par la clé privée du test ; produce/ est
// vide à la fin ; le résultat dit le nom, la taille et la somme du fichier.
func TestBackupProduce_EncryptsForThePublicKeyAndUploads(t *testing.T) {
	b := newProduceRig(t)
	stream := append([]byte("VMA\x00"), bytes.Repeat([]byte{0x00, 0xff, 'z'}, 4000)...)
	var rawMode, dirMode fs.FileMode
	inner := echoProducer(stream, nil, nil)
	b.useProducer(producer{validTarget: inner.validTarget, run: func(ctx context.Context, request producerRequest) (producerResult, error) {
		info, err := request.raw.Stat()
		if err != nil {
			t.Error(err)
		} else {
			rawMode = info.Mode().Perm()
		}
		if dir, err := os.Stat(b.produceDir()); err == nil {
			dirMode = dir.Mode().Perm()
		}
		return inner.run(ctx, request)
	}})

	results := b.run(t, 1, produceParams(b.identity))
	if len(results) != 2 || results[0].State != agentwire.ActionAccepted || results[1].State != agentwire.ActionSucceeded {
		t.Fatalf("results %+v", results)
	}
	uploaded, puts := b.server.received()
	archive, found := uploaded[producedName]
	if !found || len(uploaded) != 1 {
		t.Fatalf("the server received %v, want only %s", slices.Collect(func(yield func(string) bool) {
			for name := range uploaded {
				if !yield(name) {
					return
				}
			}
		}), producedName)
	}
	entry, content := openArchive(t, archive, b.identity)
	if entry != producedEntry || !bytes.Equal(content, stream) {
		t.Fatalf("entry %q holds %d bytes, want %q with %d", entry, len(content), producedEntry, len(stream))
	}
	want := []string{fmt.Sprintf("written %s %d sha256:%s", producedName, len(archive), sumHex(archive)), "uploaded " + producedName}
	if got := lines(results[1].Output); !slices.Equal(got, want) {
		t.Fatalf("output %q, want %q", got, want)
	}
	if len(puts) != 1 || puts[0].contentLength != int64(len(archive)) || puts[0].sum != sumHex(archive) || puts[0].expect != "100-continue" {
		t.Fatalf("what the server saw: %+v", puts)
	}
	assertDirEmpty(t, b.produceDir())
	if rawMode != 0o600 || dirMode != 0o700 {
		t.Errorf("the raw file is %v in a %v folder, want 0600 in 0700", rawMode, dirMode)
	}
	if saved, _ := LoadIdentity(b.stateDir); saved.LastOrderSeq != 1 {
		t.Errorf("seq %d", saved.LastOrderSeq)
	}
}

// Seule la clé privée ouvre l'archive : une autre clé, et l'agent lui-même qui
// n'a que la publique, n'y lisent rien. Le clair du producteur n'est nulle part
// dans le fichier téléversé.
func TestBackupProduce_TheArchiveIsUnreadableWithoutThePrivateKey(t *testing.T) {
	b := newProduceRig(t)
	secret := []byte("contenu de machine virtuelle qui ne doit jamais sortir en clair")
	b.useProducer(echoProducer(secret, nil, nil))
	if results := b.run(t, 1, produceParams(b.identity)); results[len(results)-1].State != agentwire.ActionSucceeded {
		t.Fatalf("results %+v", results)
	}
	uploaded, _ := b.server.received()
	archive := uploaded[producedName]
	if !strings.HasPrefix(string(archive), ageHeader) || bytes.Contains(archive, secret) || bytes.Contains(archive, []byte(producedEntry)) {
		t.Fatal("the uploaded archive is not an encrypted age file")
	}
	if _, err := age.Decrypt(bytes.NewReader(archive), testIdentity(t)); err == nil {
		t.Error("another key opens the archive")
	}
}

// Un producteur qui échoue : l'ordre échoue avec producer_failed, la fin de sa
// sortie d'erreur est dans le résultat, rien n'est envoyé ni laissé.
func TestBackupProduce_AFailingProducerFailsTheOrderAndLeavesNothing(t *testing.T) {
	b := newProduceRig(t)
	b.useProducer(echoProducer([]byte("début d'une archive"), []string{"INFO: starting", "ERROR: no space left in the snapshot"}, errors.New("exit status 3")))
	results := b.run(t, 1, produceParams(b.identity))
	final := results[len(results)-1]
	if final.State != agentwire.ActionFailed || final.Reason != agentwire.ReasonProducerFailed {
		t.Fatalf("results %+v", results)
	}
	if got := lines(final.Output); !slices.Equal(got, []string{"INFO: starting", "ERROR: no space left in the snapshot"}) || final.Output[1].Stream != "stderr" {
		t.Errorf("the output lacks the producer's error: %+v", final.Output)
	}
	if uploaded, puts := b.server.received(); len(uploaded) != 0 || len(puts) != 0 {
		t.Errorf("something was sent: %v %v", uploaded, puts)
	}
	assertDirEmpty(t, b.produceDir())

	// Un producteur qui sort sans rien écrire ne produit pas une archive vide.
	b.useProducer(echoProducer(nil, nil, nil))
	results = b.run(t, 2, produceParams(b.identity))
	if final := results[len(results)-1]; final.State != agentwire.ActionFailed || final.Reason != agentwire.ReasonProducerFailed {
		t.Fatalf("an empty stream: %+v", results)
	}
	assertDirEmpty(t, b.produceDir())
}

// Un envoi que le serveur refuse échoue en upload_failed, sans nouvel essai
// (il dirait la même chose) ; ce que l'agent avait écrit part.
func TestBackupProduce_ARefusedUploadFailsAndLeavesNothing(t *testing.T) {
	b := newProduceRig(t)
	b.useProducer(echoProducer([]byte("une archive"), nil, nil))
	b.server.putAnswers = []int{404}
	results := b.run(t, 1, produceParams(b.identity))
	final := results[len(results)-1]
	if final.State != agentwire.ActionFailed || final.Reason != agentwire.ReasonUploadFailed {
		t.Fatalf("results %+v", results)
	}
	output := lines(final.Output)
	if len(output) != 2 || !strings.HasPrefix(output[0], "written "+producedName) || !strings.Contains(output[1], "upload:") || !strings.Contains(output[1], "404") {
		t.Errorf("output %q", output)
	}
	if _, puts := b.server.received(); len(puts) != 1 {
		t.Errorf("the refusal was retried: %d attempts", len(puts))
	}
	assertDirEmpty(t, b.produceDir())
}

// Une coupure du réseau se relance, trois essais au plus ; l'archive est la même
// à chaque essai.
func TestBackupProduce_ARetryAfterANetworkCut(t *testing.T) {
	b := newProduceRig(t)
	b.useProducer(echoProducer([]byte("une archive"), nil, nil))
	b.server.putAnswers = []int{-1, -1}
	results := b.run(t, 1, produceParams(b.identity))
	if final := results[len(results)-1]; final.State != agentwire.ActionSucceeded {
		t.Fatalf("results %+v", results)
	}
	uploaded, puts := b.server.received()
	if len(puts) != 3 || len(uploaded) != 1 || puts[0].sum != puts[2].sum || puts[0].contentLength != puts[2].contentLength {
		t.Fatalf("puts %+v", puts)
	}
	assertDirEmpty(t, b.produceDir())

	b.server.mu.Lock()
	b.server.puts, b.server.putAnswers, b.server.uploaded = nil, []int{-1, -1, -1, -1}, nil
	b.server.mu.Unlock()
	results = b.run(t, 2, produceParams(b.identity))
	if final := results[len(results)-1]; final.State != agentwire.ActionFailed || final.Reason != agentwire.ReasonUploadFailed {
		t.Fatalf("results %+v", results)
	}
	if _, puts := b.server.received(); len(puts) != uploadAttempts {
		t.Errorf("%d attempts, want %d", len(puts), uploadAttempts)
	}
	assertDirEmpty(t, b.produceDir())
}

// Hors connexion (aucun jeton de session), l'envoi échoue en upload_failed après
// ses essais : l'agent ne tente rien sans le flux.
func TestBackupProduce_WithoutASessionTheUploadFails(t *testing.T) {
	b := newProduceRig(t)
	b.useProducer(echoProducer([]byte("une archive"), nil, nil))
	b.runner.fetch.setSession("")
	results := b.run(t, 1, produceParams(b.identity))
	if final := results[len(results)-1]; final.State != agentwire.ActionFailed || final.Reason != agentwire.ReasonUploadFailed {
		t.Fatalf("results %+v", results)
	}
	if _, puts := b.server.received(); len(puts) != 0 {
		t.Errorf("a request left without a session: %+v", puts)
	}
	assertDirEmpty(t, b.produceDir())
}

// Le juge : le droit nomme le producteur, l'agent le connaît, l'ordre est de la
// bonne forme. Rien de cela n'est accepté ni lancé quand il manque.
func TestBackupProduce_TheJudgeRefusesBeforeRunningAnything(t *testing.T) {
	b := newProduceRig(t)
	b.countingProducer([]byte("x"))

	// Sans le droit du fichier de l'agent : le refus général des ordres.
	without := newOrderBenchWithRights(t, nil)
	without.runner.receive(without.signed(t, longOrder(1, agentwire.KindBackupProduce, produceParams(b.identity))))
	if report := without.results.take(); report == nil || report.Results[0].State != agentwire.ActionRefused || report.Results[0].Reason != agentwire.RightMissing(machine.CapabilityBackupProduce) {
		t.Fatalf("without the right: %+v", report)
	}

	refused := func(seq int64, params agentwire.BackupProduceParams) agentwire.ActionResult {
		t.Helper()
		b.runner.receive(b.signed(t, longOrder(seq, agentwire.KindBackupProduce, params)))
		report := b.results.take()
		if report == nil || len(report.Results) != 1 {
			t.Fatalf("one answer expected: %+v", report)
		}
		return report.Results[0]
	}
	// Un producteur que le droit ne nomme pas, même connu de l'agent.
	other := produceParams(b.identity)
	other.Producer = "test-echo"
	b.runner.produce.producers["test-echo"] = echoProducer([]byte("x"), nil, nil)
	if got := refused(1, other); got.State != agentwire.ActionRefused || got.Reason != agentwire.RightMissing(machine.CapabilityBackupProduce) {
		t.Errorf("a producer the right does not name: %+v", got)
	}
	// Un producteur que le droit nomme et que cet agent ne sait pas lancer.
	delete(b.runner.produce.producers, machine.ProducerProxmoxVzdump)
	if got := refused(2, produceParams(b.identity)); got.State != agentwire.ActionRefused || got.Reason != agentwire.ReasonProducerUnknown {
		t.Errorf("an unknown producer: %+v", got)
	}
	b.countingProducer([]byte("x"))

	seq := int64(2)
	for name, change := range map[string]func(*agentwire.BackupProduceParams){
		"no parcel":          func(p *agentwire.BackupProduceParams) { p.Parcel = "" },
		"a long parcel":      func(p *agentwire.BackupProduceParams) { p.Parcel = strings.Repeat("a", maxParcelIDBytes+1) },
		"no prefix":          func(p *agentwire.BackupProduceParams) { p.Prefix = "" },
		"a path prefix":      func(p *agentwire.BackupProduceParams) { p.Prefix = "../pve" },
		"a capital prefix":   func(p *agentwire.BackupProduceParams) { p.Prefix = "PVE" },
		"the database":       func(p *agentwire.BackupProduceParams) { p.Prefix = agentwire.DatabaseSubject },
		"the database files": func(p *agentwire.BackupProduceParams) { p.Prefix = brand.ID },
		"no recipient":       func(p *agentwire.BackupProduceParams) { p.Recipient = "" },
		"a bad recipient":    func(p *agentwire.BackupProduceParams) { p.Recipient = "age1pasunecle" },
		"a private key":      func(p *agentwire.BackupProduceParams) { p.Recipient = "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQ" }, // gitleaks:allow -- une fausse clé privée, pour vérifier qu'elle est refusée comme destinataire.
		"no target":          func(p *agentwire.BackupProduceParams) { p.Target = "" },
		"a word target":      func(p *agentwire.BackupProduceParams) { p.Target = "all" },
		"an option target":   func(p *agentwire.BackupProduceParams) { p.Target = "--script" },
		"a command target":   func(p *agentwire.BackupProduceParams) { p.Target = "100;id" },
		"a list target":      func(p *agentwire.BackupProduceParams) { p.Target = "100,101" },
		"a negative target":  func(p *agentwire.BackupProduceParams) { p.Target = "-1" },
		"a too long target":  func(p *agentwire.BackupProduceParams) { p.Target = "1234567890" },
		"a spaced target":    func(p *agentwire.BackupProduceParams) { p.Target = "100 " },
		"a newline target":   func(p *agentwire.BackupProduceParams) { p.Target = "100\n" },
		"an unicode target":  func(p *agentwire.BackupProduceParams) { p.Target = "１００" },
		"an unknown nothing": func(p *agentwire.BackupProduceParams) { p.Producer = "" },
	} {
		params := produceParams(b.identity)
		change(&params)
		wantReason, wantState := agentwire.ReasonBadOrder, agentwire.ActionFailed
		if name == "an unknown nothing" {
			wantReason, wantState = agentwire.RightMissing(machine.CapabilityBackupProduce), agentwire.ActionRefused
		}
		seq++
		if got := refused(seq, params); got.State != wantState || got.Reason != wantReason {
			t.Errorf("%s: %+v", name, got)
		}
	}
	if len(b.produced) != 0 {
		t.Errorf("the producer ran for %v", b.produced)
	}
	if _, puts := b.server.received(); len(puts) != 0 {
		t.Errorf("something was sent: %+v", puts)
	}
}

// La machine ne laisse pas lancer le producteur (pas de sudo, règle absente,
// script manquant) : l'ordre est accepté puis refusé avant la première écriture,
// avec la même raison que le rapport des droits indisponibles. produce/ n'est
// même pas créé.
func TestBackupProduce_RefusesBeforeWritingWhenTheMachineCannotRunIt(t *testing.T) {
	cases := map[string]struct {
		change func(*hostChecks)
		reason string
	}{
		"no sudo": {func(c *hostChecks) {
			c.lookPath = func(name string) (string, error) {
				if name == "sudo" {
					return "", errors.New("not found")
				}
				return name, nil
			}
		}, agentwire.ReasonSudoMissing},
		"no wrapper": {func(c *hostChecks) {
			c.lookPath = func(name string) (string, error) {
				if name == brand.VzdumpWrapperPath {
					return "", errors.New("not found")
				}
				return name, nil
			}
		}, agentwire.ReasonProducerMissing},
		"no sudo rule": {func(c *hostChecks) {
			c.sudoList = func(context.Context, string) error { return errors.New("sudo: a password is required") }
		}, agentwire.ReasonSudoDenied},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := newProduceRig(t)
			b.countingProducer([]byte("x"))
			checks := allowAll()
			c.change(&checks)
			useHostChecks(b.runner, checks)
			results := b.run(t, 1, produceParams(b.identity))
			if len(results) != 2 || results[1].State != agentwire.ActionRefused || results[1].Reason != c.reason {
				t.Fatalf("results %+v", results)
			}
			if len(b.produced) != 0 {
				t.Errorf("the producer ran")
			}
			if _, err := os.Lstat(b.produceDir()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("produce/ was created: %v", err)
			}
		})
	}
}

// Moins de place libre que la réserve : refusé avant de lancer quoi que ce soit.
// Un disque qui se remplit en route est un échec disk_full, pas un refus.
func TestBackupProduce_ADiskTooFullIsRefusedAndAFullingDiskFails(t *testing.T) {
	b := newProduceRig(t)
	b.countingProducer([]byte("x"))
	b.runner.produce.freeBytes = func(string) (uint64, error) { return minProduceFreeBytes - 1, nil }
	results := b.run(t, 1, produceParams(b.identity))
	if final := results[len(results)-1]; final.State != agentwire.ActionRefused || final.Reason != agentwire.ReasonDiskFull {
		t.Fatalf("results %+v", results)
	}
	if len(b.produced) != 0 {
		t.Error("the producer ran on a full disk")
	}
	if _, err := os.Lstat(b.produceDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("produce/ was created: %v", err)
	}

	// Exactement la réserve passe.
	b.runner.produce.freeBytes = func(string) (uint64, error) { return minProduceFreeBytes, nil }
	if results := b.run(t, 2, produceParams(b.identity)); results[len(results)-1].State != agentwire.ActionSucceeded {
		t.Fatalf("exactly the reserve: %+v", results)
	}

	for _, err := range []error{syscall.ENOSPC, fmt.Errorf("write archive: %w", syscall.ENOSPC)} {
		if result := resultOf("run", nil, writeFailure(err)); result.State != agentwire.ActionFailed || result.Reason != agentwire.ReasonDiskFull {
			t.Errorf("%v: %+v", err, result)
		}
	}
	if result := resultOf("run", nil, writeFailure(errors.New("read-only file system"))); result.Reason != agentwire.ReasonPathUnwritable {
		t.Errorf("another write failure: %+v", result)
	}
}

// Ce qu'un arrêt en pleine production a laissé dans produce/ part au démarrage,
// sous-dossier compris ; un dossier absent n'est pas une erreur.
func TestClearProduceDir_RemovesWhatAKillLeftBehind(t *testing.T) {
	state, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := clearProduceDir(state); err != nil {
		t.Fatalf("no produce dir: %v", err)
	}
	if err := state.MkdirAll("produce/reste", 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"produce/" + producedName + ".raw", "produce/" + producedName + ".part", "produce/" + producedName, "produce/reste/x"} {
		if err := state.WriteFile(name, []byte("de quoi remplir un disque"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.WriteFile("identity.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearProduceDir(state); err != nil {
		t.Fatal(err)
	}
	assertDirEmpty(t, filepath.Join(state.Name(), "produce"))
	if _, err := state.Stat("identity.json"); err != nil {
		t.Errorf("the rest of the state went with it: %v", err)
	}
}

// Un produce/ qui est un lien n'est ni vidé au démarrage ni écrit par un
// ordre : os.Root suivrait un lien vers l'état lui-même, et vider produce/
// emporterait la clé de l'agent.
func TestBackupProduce_AProduceDirThatIsALinkIsNeverEmptied(t *testing.T) {
	b := newProduceRig(t)
	b.countingProducer([]byte("x"))
	if err := b.stateDir.WriteFile("identity.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", b.produceDir()); err != nil {
		t.Fatal(err)
	}
	if err := clearProduceDir(b.stateDir); err == nil {
		t.Error("a linked produce dir was cleared")
	}
	results := b.run(t, 1, produceParams(b.identity))
	if final := results[len(results)-1]; final.State != agentwire.ActionRefused || final.Reason != agentwire.ReasonPathUnwritable {
		t.Fatalf("results %+v", results)
	}
	if len(b.produced) != 0 {
		t.Error("the producer ran into a linked produce dir")
	}
	if _, err := b.stateDir.Stat("identity.json"); err != nil {
		t.Fatalf("the state went with produce/: %v", err)
	}
}

// Un reste du même nom dans produce/ (un .raw que la coupure a laissé) ne bloque
// pas la production suivante.
func TestBackupProduce_ALeftoverOfTheSameNameDoesNotBlock(t *testing.T) {
	b := newProduceRig(t)
	b.useProducer(echoProducer([]byte("une archive"), nil, nil))
	if err := b.stateDir.MkdirAll("produce", 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{producedName + rawSuffix, producedName + partSuffix} {
		if err := b.stateDir.WriteFile("produce/"+name, []byte("reste"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if results := b.run(t, 1, produceParams(b.identity)); results[len(results)-1].State != agentwire.ActionSucceeded {
		t.Fatalf("results %+v", results)
	}
	assertDirEmpty(t, b.produceDir())
}

// recordingSink garde ce que l'envoi voit dans produce/ au moment de partir.
type recordingSink struct {
	dir     string
	seen    []string
	size    int64
	sum     string
	content []byte
}

func (s *recordingSink) put(_ context.Context, _ string, file parcelUpload) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		s.seen = append(s.seen, entry.Name())
	}
	body, err := file.open()
	if err != nil {
		return err
	}
	defer body.Close()
	s.content, err = io.ReadAll(body)
	s.size, s.sum = file.size, file.sum
	return err
}

// Quand l'archive part, elle est seule dans produce/ : le brut est déjà effacé
// et le .part renommé. Deux archives de la taille d'une machine virtuelle ne
// restent pas ensemble pendant l'envoi.
func TestBackupProduce_OnlyTheFinalArchiveIsThereWhileUploading(t *testing.T) {
	b := newProduceRig(t)
	state := b.stateDir
	tools := newProduceTools(state)
	tools.producers[machine.ProducerProxmoxVzdump] = echoProducer([]byte("une archive"), nil, nil)
	tools.freeBytes = b.runner.produce.freeBytes
	sink := &recordingSink{dir: filepath.Join(state.Name(), "produce")}
	job, refused := readBackupProduce(longOrder(1, agentwire.KindBackupProduce, produceParams(b.identity)), b.runner.identity.Capabilities(), tools.producers)
	if refused != nil {
		t.Fatalf("refused: %+v", refused)
	}
	output, err := tools.produce(context.Background(), allowAll(), sink, job, orderNow)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sink.seen, []string{producedName}) {
		t.Fatalf("produce/ held %v while uploading", sink.seen)
	}
	if int64(len(sink.content)) != sink.size || sumHex(sink.content) != sink.sum {
		t.Errorf("the file read back is not the one announced")
	}
	if got := lines(output); len(got) != 2 || got[0] != fmt.Sprintf("written %s %d sha256:%s", producedName, sink.size, sink.sum) {
		t.Errorf("output %q", got)
	}
}

// Le flux du producteur n'est jamais chargé en mémoire : 96 Mio passent par le
// tar, le chiffrement et la somme, et l'agent n'alloue qu'une fraction de cette
// taille en tout (des tampons réutilisés), pas un tableau de la taille du flux.
func TestBackupProduce_AHugeStreamIsNeverHeldInMemory(t *testing.T) {
	const size = 96 << 20
	dir, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	raw, err := dir.OpenFile(producedName+rawSuffix, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	for written := 0; written < size; written += len(chunk) {
		if _, err := raw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	identity := testIdentity(t)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	file, err := packArchive(dir, producedName, packEntry{name: producedEntry, content: raw, size: size}, identity.Recipient())
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > size/3 {
		t.Errorf("packing %d bytes allocated %d: the stream is being held in memory", size, allocated)
	}
	if file.Size < size {
		t.Fatalf("archive of %d bytes for a stream of %d", file.Size, size)
	}
	if _, err := dir.Stat(producedName + rawSuffix); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the raw file is still there: %v", err)
	}
	// L'archive se déchiffre jusqu'au bout et rend le flux entier.
	archive, err := dir.Open(producedName)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	decrypted, err := age.Decrypt(archive, identity)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(decrypted)
	header, err := reader.Next()
	if err != nil || header.Name != producedEntry || header.Size != size {
		t.Fatalf("entry %+v %v", header, err)
	}
	if copied, err := io.Copy(io.Discard, reader); err != nil || copied != size {
		t.Fatalf("copied %d: %v", copied, err)
	}
}

// Le producteur proxmox-vzdump lance sudo -n sur le script de root avec des
// arguments constants et la cible ; l'environnement est remplacé, jamais
// hérité ; sa sortie standard est l'archive, sa sortie d'erreur le journal. Le
// vrai sudo n'est jamais lancé : le test donne un faux sudo et un faux script.
type fakeVzdump struct {
	sudo, wrapper, record, environment string
}

func newFakeVzdump(t *testing.T, wrapperBody string) fakeVzdump {
	t.Helper()
	dir := t.TempDir()
	fake := fakeVzdump{
		sudo: filepath.Join(dir, "sudo"), wrapper: filepath.Join(dir, "opencloud-vzdump"),
		record: filepath.Join(dir, "arguments"), environment: filepath.Join(dir, "environment"),
	}
	// Le faux sudo note ses arguments puis lance ce qui suit « -n », comme le
	// ferait le vrai sudo avec une règle NOPASSWD.
	sudo := "#!/bin/sh\nfor argument in \"$@\"; do printf '%s\\n' \"$argument\" >> " + fake.record + "; done\nshift\nexec \"$@\"\n"
	wrapper := "#!/bin/sh\n/usr/bin/env > " + fake.environment + "\n" + wrapperBody + "\n"
	for path, body := range map[string]string{fake.sudo: sudo, fake.wrapper: wrapper} {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil { // #nosec G306 -- un faux programme exécutable dans un dossier de test.
			t.Fatal(err)
		}
	}
	return fake
}

func (f fakeVzdump) producer() producer {
	runner := vzdumpProducer{sudo: f.sudo, wrapper: f.wrapper}
	return producer{validTarget: vzdumpTarget.MatchString, run: runner.run}
}

func (f fakeVzdump) arguments(t *testing.T) []string {
	t.Helper()
	recorded, err := os.ReadFile(f.record) // #nosec G304 G703 -- le fichier du faux sudo, dans le dossier du test.
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(recorded))
}

func TestVzdumpProducer_RunsSudoOnTheWrapperAndStreamsItsOutput(t *testing.T) {
	t.Setenv("OPENCLOUD_TEST_LEAK", "ne-doit-pas-passer")
	fake := newFakeVzdump(t, `printf 'VMA\000\377octets de vzdump' ; printf 'INFO: Starting Backup of VM 5012 (qemu)\nINFO: transferred 1 GiB\n' >&2`)
	b := newProduceRig(t)
	b.useProducer(fake.producer())
	results := b.run(t, 1, produceParams(b.identity))
	if len(results) != 2 || results[1].State != agentwire.ActionSucceeded {
		t.Fatalf("results %+v", results)
	}
	wantArguments := []string{"-n", fake.wrapper, "5012", "--mode", "snapshot", "--compress", "zstd", "--stdout", "--quiet", "1"}
	if got := fake.arguments(t); !slices.Equal(got, wantArguments) {
		t.Errorf("sudo ran with %q, want %q", got, wantArguments)
	}
	environment, err := os.ReadFile(fake.environment) // #nosec G304 G703 -- le fichier du faux script, dans le dossier du test.
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"} {
		if !strings.Contains(string(environment), want+"\n") {
			t.Errorf("the producer's environment lacks %q:\n%s", want, environment)
		}
	}
	if strings.Contains(string(environment), "OPENCLOUD_TEST_LEAK") {
		t.Errorf("the producer inherited the agent's environment:\n%s", environment)
	}
	uploaded, _ := b.server.received()
	entry, content := openArchive(t, uploaded[producedName], b.identity)
	if entry != producedEntry || string(content) != "VMA\x00\xffoctets de vzdump" {
		t.Errorf("entry %q holds %q", entry, content)
	}
}

// Le type de l'invité nomme l'entrée du tar : la ligne de vzdump qui le dit
// d'abord ; à défaut, lxc si une ligne en parle, sinon une machine virtuelle.
func TestVzdumpProducer_NamesTheEntryAfterTheGuestType(t *testing.T) {
	for name, c := range map[string]struct{ stderr, entry string }{
		"a container":          {`printf 'INFO: Starting Backup of VM 5012 (lxc)\n' >&2`, "vzdump-lxc-5012-2026_10_21-12_00_00.tar.zst"},
		"a virtual machine":    {`printf 'INFO: Starting Backup of VM 5012 (qemu)\n' >&2`, "vzdump-qemu-5012-2026_10_21-12_00_00.vma.zst"},
		"no line at all":       {`:`, "vzdump-qemu-5012-2026_10_21-12_00_00.vma.zst"},
		"lxc without the line": {`printf 'INFO: creating archive from lxc rootfs\n' >&2`, "vzdump-lxc-5012-2026_10_21-12_00_00.tar.zst"},
		"the line wins":        {`printf 'INFO: lxc is mentioned\nINFO: Starting Backup of VM 5012 (qemu)\n' >&2`, "vzdump-qemu-5012-2026_10_21-12_00_00.vma.zst"},
		"no end of line":       {`printf 'INFO: Starting Backup of VM 5012 (lxc)' >&2`, "vzdump-lxc-5012-2026_10_21-12_00_00.tar.zst"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeVzdump(t, "printf 'octets'\n"+c.stderr)
			raw, err := os.CreateTemp(t.TempDir(), "raw")
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			result, err := fake.producer().run(context.Background(), producerRequest{target: "5012", raw: raw, now: orderNow})
			if err != nil || result.entry != c.entry {
				t.Fatalf("entry %q (%v), want %q", result.entry, err, c.entry)
			}
		})
	}
}

// vzdump qui sort en erreur : l'ordre échoue en producer_failed, sa sortie
// d'erreur est dans le résultat, rien n'est envoyé.
func TestVzdumpProducer_AFailingVzdumpFailsTheOrder(t *testing.T) {
	fake := newFakeVzdump(t, "printf 'début' ; printf 'INFO: starting\\nERROR: Backup of VM 5012 failed - no such VM\\n' >&2 ; exit 3")
	b := newProduceRig(t)
	b.useProducer(fake.producer())
	results := b.run(t, 1, produceParams(b.identity))
	final := results[len(results)-1]
	if final.State != agentwire.ActionFailed || final.Reason != agentwire.ReasonProducerFailed {
		t.Fatalf("results %+v", results)
	}
	if got := lines(final.Output); !slices.Equal(got, []string{"INFO: starting", "ERROR: Backup of VM 5012 failed - no such VM"}) {
		t.Errorf("output %q", got)
	}
	if uploaded, _ := b.server.received(); len(uploaded) != 0 {
		t.Errorf("something was sent: %v", uploaded)
	}
	assertDirEmpty(t, b.produceDir())
}

// Un producteur qui n'en finit pas s'arrête avec le contexte : sudo reçoit le
// signal d'arrêt, l'ordre échoue, produce/ est vide.
func TestVzdumpProducer_ACancelledOrderStopsTheProducer(t *testing.T) {
	fake := newFakeVzdump(t, "exec sleep 30")
	b := newProduceRig(t)
	tools := b.runner.produce
	tools.producers = map[string]producer{machine.ProducerProxmoxVzdump: fake.producer()}
	job, refused := readBackupProduce(longOrder(1, agentwire.KindBackupProduce, produceParams(b.identity)), b.runner.identity.Capabilities(), tools.producers)
	if refused != nil {
		t.Fatalf("refused: %+v", refused)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := tools.produce(ctx, allowAll(), &recordingSink{dir: b.produceDir()}, job, orderNow)
	if result := resultOf("run", nil, err); result.State != agentwire.ActionFailed || result.Reason != agentwire.ReasonProducerFailed {
		t.Fatalf("result %+v (%v)", result, err)
	}
	if time.Since(started) > 10*time.Second {
		t.Errorf("the producer was not stopped: %v", time.Since(started))
	}
	assertDirEmpty(t, b.produceDir())
}

// Le vrai programme sudo n'est jamais lancé par les tests : sans sudo donné, le
// producteur le cherche dans le PATH, et un PATH sans sudo est un échec
// d'exécution, pas un lancement.
func TestVzdumpProducer_WithoutSudoNothingRuns(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	raw, err := os.CreateTemp(t.TempDir(), "raw")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := (vzdumpProducer{wrapper: "/nulle/part"}).run(context.Background(), producerRequest{target: "5012", raw: raw, now: orderNow}); err == nil {
		t.Fatal("a producer ran without sudo")
	}
}
