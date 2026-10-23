package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/release"
	"github.com/ldesfontaine/opencloud/internal/release/releasetest"
)

const archBinary = brand.ReleaseBinaryPrefix + "amd64"

// updateBench est un agent installé comme la doc le dit : <état>/bin/ porte
// opencloud-v0.0.1 et le lien current qui y mène. Les versions se signent avec
// une clé tirée ici, que l'agent embarque ; l'épreuve est factice.
type updateBench struct {
	stateDir  *os.Root
	state     string
	updater   *selfUpdater
	public    release.PublicKey
	private   release.PrivateKey
	restarted int
	checked   []string
	checkErr  error
}

func newUpdateBench(t *testing.T, ownVersion string) *updateBench {
	t.Helper()
	public, private, err := release.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	b := &updateBench{stateDir: root, state: state, public: public, private: private}
	if err := root.MkdirAll(brand.AgentBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("bin/opencloud-v0.0.1", []byte("the running binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("opencloud-v0.0.1", "bin/current"); err != nil {
		t.Fatal(err)
	}
	b.updater = newSelfUpdater(root, ownVersion, slog.New(slog.DiscardHandler))
	b.updater.key, b.updater.hasKey = public, true
	b.updater.executable = func() (string, error) { return filepath.Join(state, "bin", "opencloud-v0.0.1"), nil }
	b.updater.selfCheck = func(_ context.Context, binary, version string) error {
		b.checked = append(b.checked, binary+" "+version)
		return b.checkErr
	}
	b.updater.restart = func() { b.restarted++ }
	return b
}

// published est une version signée telle que le serveur la tend : le colis
// avec ses trois fichiers, et les paramètres de l'ordre qui le décrit.
type published struct {
	server *parcelServer
	params agentwire.AgentUpdateParams
}

// publish signe version avec la clé de l'agent ; signedVersion est celle que
// la signature dit, pour les tests qui la trompent.
func (b *updateBench) publish(t *testing.T, version, signedVersion string, signer release.PrivateKey) published {
	t.Helper()
	binary := "the binary of " + version
	digest := sha256.Sum256([]byte(binary))
	sums := hex.EncodeToString(digest[:]) + "  " + archBinary + "\n"
	signature, err := release.Sign([]byte(sums), signer, "timestamp:1\tfile:"+brand.ReleaseSumsFile+"\tversion:"+signedVersion)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{archBinary: binary, brand.ReleaseSumsFile: sums, brand.ReleaseSignatureFile: string(signature)}
	return published{
		server: &parcelServer{files: files},
		params: agentwire.AgentUpdateParams{Version: version, Parcel: "colis1", Binary: archBinary, Files: listed(files)},
	}
}

func (b *updateBench) update(t *testing.T, p published) error {
	t.Helper()
	return b.updater.update(context.Background(), sourceFor(t, p.server), p.params)
}

func (b *updateBench) current(t *testing.T) string {
	t.Helper()
	target, err := b.stateDir.Readlink("bin/current")
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// untouched : l'agent est resté sur son binaire, rien n'est resté de la
// tentative.
func (b *updateBench) untouched(t *testing.T) {
	t.Helper()
	if target := b.current(t); target != "opencloud-v0.0.1" {
		t.Fatalf("current points to %s", target)
	}
	entries, err := os.ReadDir(filepath.Join(b.state, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, " ") != "current opencloud-v0.0.1" {
		t.Fatalf("bin holds %v", names)
	}
	if b.restarted != 0 {
		t.Fatal("asked for a restart without a swap")
	}
}

func reasonOf(t *testing.T, err error) string {
	t.Helper()
	var failed *orderError
	if !errors.As(err, &failed) {
		t.Fatalf("not an order failure: %v", err)
	}
	return failed.reason
}

func TestAgentUpdate_InstallsVerifiesSwapsAndAsksForARestart(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	// Des restes de ce que l'agent a posé avant : une vieille version et un
	// dossier d'arrivée d'une mise à jour coupée disparaissent ; le reste de
	// bin/ n'est pas à l'agent.
	if err := b.stateDir.WriteFile("bin/opencloud-v0.0.0", []byte("older"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.MkdirAll("bin/.incoming-v0.0.9", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.WriteFile("bin/notes.txt", []byte("not ours"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := b.update(t, b.publish(t, "v0.0.2", "v0.0.2", b.private)); err != nil {
		t.Fatal(err)
	}

	if target := b.current(t); target != "opencloud-v0.0.2" {
		t.Fatalf("current points to %s", target)
	}
	installed, err := b.stateDir.Stat("bin/opencloud-v0.0.2")
	if err != nil || installed.Mode().Perm() != 0o755 {
		t.Fatalf("installed %v %v", installed, err)
	}
	if content, _ := b.stateDir.ReadFile("bin/opencloud-v0.0.2"); string(content) != "the binary of v0.0.2" {
		t.Fatalf("installed %q", content)
	}
	if len(b.checked) != 1 || b.checked[0] != filepath.Join(b.state, "bin", "opencloud-v0.0.2")+" v0.0.2" {
		t.Fatalf("checked %v", b.checked)
	}
	// L'épreuve est passée avant la bascule : au moment où elle a tourné, le
	// lien visait encore l'ancienne version (testé plus bas par l'échec).
	for _, kept := range []string{"bin/opencloud-v0.0.1", "bin/notes.txt"} {
		if _, err := b.stateDir.Stat(kept); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	for _, gone := range []string{"bin/opencloud-v0.0.0", "bin/.incoming-v0.0.9", "bin/.incoming-v0.0.2", "bin/current.new"} {
		if _, err := b.stateDir.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there (%v)", gone, err)
		}
	}
}

func TestAgentUpdate_RefusesADowngrade(t *testing.T) {
	b := newUpdateBench(t, "v0.0.5")
	err := b.update(t, b.publish(t, "v0.0.2", "v0.0.2", b.private))
	if reason := reasonOf(t, err); reason != agentwire.ReasonDowngrade {
		t.Fatalf("reason %s", reason)
	}
	b.untouched(t)
}

// La même version n'a rien à faire : la réinstaller écraserait le binaire qui
// tourne.
func TestAgentUpdate_RefusesItsOwnVersion(t *testing.T) {
	b := newUpdateBench(t, "v0.0.2")
	if reason := reasonOf(t, b.update(t, b.publish(t, "v0.0.2", "v0.0.2", b.private))); reason != agentwire.ReasonDowngrade {
		t.Fatalf("reason %s", reason)
	}
	b.untouched(t)
	if len(b.checked) != 0 {
		t.Fatal("the same version was run")
	}
}

// Même quand sa propre version ne se lit pas, l'agent n'écrit jamais par-dessus
// le binaire que current lance : une épreuve ratée l'effacerait.
func TestAgentUpdate_NeverReplacesTheBinaryCurrentRuns(t *testing.T) {
	b := newUpdateBench(t, "dev")
	b.checkErr = errors.New("exec format error")
	if reason := reasonOf(t, b.update(t, b.publish(t, "v0.0.1", "v0.0.1", b.private))); reason != agentwire.ReasonDowngrade {
		t.Fatalf("reason %s", reason)
	}
	b.untouched(t)
	if content, _ := b.stateDir.ReadFile("bin/opencloud-v0.0.1"); string(content) != "the running binary" {
		t.Fatalf("the running binary became %q", content)
	}
}

// Un lien current posé à la main en chemin absolu garde sa cible comme
// précédente : le retour en arrière reste possible.
func TestAgentUpdate_KeepsThePreviousOfAnAbsoluteLink(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	if err := b.stateDir.Remove("bin/current"); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.Symlink(filepath.Join(b.state, "bin", "opencloud-v0.0.1"), "bin/current"); err != nil {
		t.Fatal(err)
	}
	if err := b.update(t, b.publish(t, "v0.0.2", "v0.0.2", b.private)); err != nil {
		t.Fatal(err)
	}
	if target := b.current(t); target != "opencloud-v0.0.2" {
		t.Fatalf("current points to %s", target)
	}
	if _, err := b.stateDir.Stat("bin/opencloud-v0.0.1"); err != nil {
		t.Fatalf("the previous version was removed: %v", err)
	}
}

func TestAgentUpdate_AnOwnVersionThatDoesNotParseIsOlderThanAnyRelease(t *testing.T) {
	b := newUpdateBench(t, "dev")
	if err := b.update(t, b.publish(t, "v0.0.2", "v0.0.2", b.private)); err != nil {
		t.Fatal(err)
	}
	if target := b.current(t); target != "opencloud-v0.0.2" {
		t.Fatalf("current points to %s", target)
	}
}

func TestAgentUpdate_RefusesAnotherSignature(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	_, stranger, err := release.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if reason := reasonOf(t, b.update(t, b.publish(t, "v0.0.2", "v0.0.2", stranger))); reason != agentwire.ReasonSignatureInvalid {
		t.Fatalf("reason %s", reason)
	}
	b.untouched(t)
	if len(b.checked) != 0 {
		t.Fatal("a binary signed by another key was run")
	}
}

func TestAgentUpdate_RefusesWithoutAnEmbeddedKey(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	b.updater.hasKey = false
	published := b.publish(t, "v0.0.2", "v0.0.2", b.private)
	if reason := reasonOf(t, b.update(t, published)); reason != agentwire.ReasonReleaseKeyMissing {
		t.Fatalf("reason %s", reason)
	}
	if len(published.server.requests) != 0 {
		t.Fatalf("downloaded without a key: %v", published.server.requests)
	}
	b.untouched(t)
}

func TestAgentUpdate_RefusesAVersionSignedForAnother(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	// Un vieux colis signé (v0.0.2) présenté pour la version demandée (v0.0.9).
	if reason := reasonOf(t, b.update(t, b.publish(t, "v0.0.9", "v0.0.2", b.private))); reason != agentwire.ReasonVersionMismatch {
		t.Fatalf("reason %s", reason)
	}
	b.untouched(t)
}

func TestAgentUpdate_RefusesABinaryThatIsNotTheSignedOne(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	published := b.publish(t, "v0.0.2", "v0.0.2", b.private)
	// Le serveur signe l'ordre avec la somme d'un autre binaire que celui des
	// sommes signées : la somme du colis est juste, celle de la version non.
	// Un seul octet diffère (« v0.0.2 » devenu « v0.0.3 »).
	swapped := "the binary of v0.0.3"
	published.server.files[archBinary] = swapped
	published.params.Files = listed(published.server.files)
	if reason := reasonOf(t, b.update(t, published)); reason != agentwire.ReasonDigestMismatch {
		t.Fatalf("reason %s", reason)
	}
	b.untouched(t)
}

func TestAgentUpdate_RefusesADownloadThatDoesNotMatchTheOrder(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	published := b.publish(t, "v0.0.2", "v0.0.2", b.private)
	published.server.files[archBinary] += " tampered in transit"
	if reason := reasonOf(t, b.update(t, published)); reason != agentwire.ReasonDigestMismatch {
		t.Fatalf("reason %s", reason)
	}
	b.untouched(t)

	delete(published.server.files, brand.ReleaseSignatureFile)
	if reason := reasonOf(t, b.update(t, published)); reason != agentwire.ReasonDownloadFailed {
		t.Fatalf("a missing file: reason %s", reason)
	}
	b.untouched(t)
}

func TestAgentUpdate_SelfCheckFailureKeepsTheOldLink(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	b.checkErr = errors.New("exec format error")
	if reason := reasonOf(t, b.update(t, b.publish(t, "v0.0.2", "v0.0.2", b.private))); reason != agentwire.ReasonSelfCheckFailed {
		t.Fatalf("reason %s", reason)
	}
	if len(b.checked) != 1 {
		t.Fatalf("checked %v", b.checked)
	}
	b.untouched(t)
}

func TestSelfUpdatable_OnlyFromItsOwnBin(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	if !b.updater.isSelfUpdatable() {
		t.Fatal("an agent running from bin/ is updatable")
	}
	// Lancé par le lien current, comme l'unité le fait.
	b.updater.executable = func() (string, error) { return filepath.Join(b.state, "bin", "current"), nil }
	if !b.updater.isSelfUpdatable() {
		t.Fatal("the current link leads to bin/")
	}
	outside := filepath.Join(t.TempDir(), "opencloud")
	if err := os.WriteFile(outside, []byte("elsewhere"), 0o755); err != nil { // #nosec G306 -- un fichier de test, exécutable
		t.Fatal(err)
	}
	b.updater.executable = func() (string, error) { return outside, nil }
	if b.updater.isSelfUpdatable() {
		t.Fatal("an agent run from /usr/local/bin-like path is not updatable")
	}
	// Un lien posé dans bin/ vers un binaire du dehors ne rend pas l'agent
	// mettable à jour : ce qui compte est le fichier réel.
	if err := b.stateDir.Symlink(outside, "bin/elsewhere"); err != nil {
		t.Fatal(err)
	}
	b.updater.executable = func() (string, error) { return filepath.Join(b.state, "bin", "elsewhere"), nil }
	if b.updater.isSelfUpdatable() {
		t.Fatal("a link to the outside is not updatable")
	}
	b.updater.executable = func() (string, error) { return "", errors.New("no /proc") }
	if b.updater.isSelfUpdatable() {
		t.Fatal("an unknown executable is not updatable")
	}
}

// Le juge : refus immédiat hors de bin/, mal formé, ou accepté puis fait par
// le worker, qui demande la relance sans rien rapporter.
func TestOrderRunner_AgentUpdateIsRefusedAcceptedOrMalformed(t *testing.T) {
	b := newOrderBenchWithRights(t, []string{"observe", "agent.update"})
	updateOrder := func(seq int64, params any) agentwire.OrderBody {
		body := order(seq)
		body.Kind = agentwire.KindAgentUpdate
		body.Params, _ = json.Marshal(params)
		return body
	}
	// Ce binaire de test ne tourne pas depuis le bin/ de l'état.
	result := b.send(t, updateOrder(1, nil), b.private)
	if result.State != agentwire.ActionRefused || result.Reason != agentwire.ReasonNotSelfUpdatable {
		t.Fatalf("outside bin: %+v", result)
	}

	update := newUpdateBench(t, "v0.0.1")
	b.runner.updater = update.updater
	published := update.publish(t, "v0.0.2", "v0.0.2", update.private)
	seq := int64(10)
	for name, params := range map[string]any{
		"no params":       nil,
		"a bad version":   agentwire.AgentUpdateParams{Version: "2", Parcel: "colis1", Binary: archBinary, Files: published.params.Files},
		"no parcel":       agentwire.AgentUpdateParams{Version: "v0.0.2", Binary: archBinary, Files: published.params.Files},
		"a path as name":  agentwire.AgentUpdateParams{Version: "v0.0.2", Parcel: "colis1", Binary: "../" + archBinary, Files: published.params.Files},
		"a missing file":  agentwire.AgentUpdateParams{Version: "v0.0.2", Parcel: "colis1", Binary: archBinary, Files: published.params.Files[:2]},
		"a surplus file":  agentwire.AgentUpdateParams{Version: "v0.0.2", Parcel: "colis1", Binary: archBinary, Files: append(append([]agentwire.ParcelFile{}, published.params.Files...), agentwire.ParcelFile{Name: "extra", SHA256: published.params.Files[0].SHA256})},
		"a huge binary":   hugeBinary(published.params),
		"a bad sum":       withSum(published.params, "not a sum"),
		"a duplicate":     agentwire.AgentUpdateParams{Version: "v0.0.2", Parcel: "colis1", Binary: archBinary, Files: []agentwire.ParcelFile{published.params.Files[0], published.params.Files[0], published.params.Files[1]}},
		"another binary":  agentwire.AgentUpdateParams{Version: "v0.0.2", Parcel: "colis1", Binary: brand.ReleaseBinaryPrefix + "arm64", Files: published.params.Files},
		"a sum too large": tooLargeSums(published.params),
	} {
		seq++
		result := b.send(t, updateOrder(seq, params), b.private)
		if result.State != agentwire.ActionFailed || result.Reason != agentwire.ReasonBadOrder {
			t.Errorf("%s: %+v", name, result)
		}
	}

	seq++
	body := updateOrder(seq, published.params)
	result, job := b.runner.execute(body)
	if result.State != agentwire.ActionAccepted || job == nil {
		t.Fatalf("a good order: %+v", result)
	}
	b.runner.fetch = sourceFor(t, published.server)
	job(context.Background())
	if update.restarted != 1 || update.current(t) != "opencloud-v0.0.2" {
		t.Fatalf("restarted %d, current %s", update.restarted, update.current(t))
	}
	// Réussie, la mise à jour ne rapporte rien de plus que « accepté » : le
	// serveur attend le retour de l'agent.
	if report := b.results.take(); report != nil {
		t.Fatalf("a finished swap reported %+v", report)
	}
}

func hugeBinary(params agentwire.AgentUpdateParams) agentwire.AgentUpdateParams {
	files := append([]agentwire.ParcelFile{}, params.Files...)
	for index := range files {
		if files[index].Name == params.Binary {
			files[index].Size = maxUpdateBinaryBytes + 1
		}
	}
	params.Files = files
	return params
}

func tooLargeSums(params agentwire.AgentUpdateParams) agentwire.AgentUpdateParams {
	files := append([]agentwire.ParcelFile{}, params.Files...)
	for index := range files {
		if files[index].Name == brand.ReleaseSumsFile {
			files[index].Size = maxSignedFileBytes + 1
		}
	}
	params.Files = files
	return params
}

func withSum(params agentwire.AgentUpdateParams, sum string) agentwire.AgentUpdateParams {
	files := append([]agentwire.ParcelFile{}, params.Files...)
	files[0].SHA256 = sum
	params.Files = files
	return params
}

// Le binaire de l'épreuve est un script de test : rien en production ne lance
// de shell. Il dit la version demandée, et échoue si l'agent lui a laissé son
// environnement.
func TestSelfCheck_RunsTheBinaryWithAReplacedEnvironment(t *testing.T) {
	t.Setenv("OPENCLOUD_CANARY", "from the agent")
	write := func(name, script string) string {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil { // #nosec G306 -- un script de test, exécutable
			t.Fatal(err)
		}
		return path
	}
	good := write("good", "#!/bin/sh\n[ -z \"$OPENCLOUD_CANARY\" ] || exit 7\n[ \"$LC_ALL\" = C ] || exit 8\necho 'opencloud v0.0.2 (test)'\n")
	if err := runSelfCheck(context.Background(), good, "v0.0.2"); err != nil {
		t.Fatalf("good binary: %v", err)
	}
	cases := map[string]string{
		"another version":                    "#!/bin/sh\necho 'opencloud v0.0.3 (test)'\n",
		"no output":                          "#!/bin/sh\n",
		"fails":                              "#!/bin/sh\necho 'opencloud v0.0.2'\nexit 1\n",
		"the version is not the second word": "#!/bin/sh\necho 'v0.0.2'\n",
	}
	for name, script := range cases {
		if err := runSelfCheck(context.Background(), write(name, script), "v0.0.2"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Une sortie sans fin ne remplit pas la mémoire : elle est bornée, et la
	// version, dite avant, est lue.
	loud := write("loud", "#!/bin/sh\necho 'opencloud v0.0.2 (test)'\nyes | head -c 1000000\n")
	if err := runSelfCheck(context.Background(), loud, "v0.0.2"); err != nil {
		t.Fatalf("a talkative binary: %v", err)
	}
	if err := runSelfCheck(context.Background(), filepath.Join(t.TempDir(), "absent"), "v0.0.2"); err == nil {
		t.Fatal("a missing binary was accepted")
	}
}

func TestCappedBuffer_KeepsTheStartAndNeverFailsAWrite(t *testing.T) {
	var buffer cappedBuffer
	big := strings.Repeat("a", selfCheckOutput+500)
	written, err := buffer.Write([]byte(big))
	if err != nil || written != len(big) || len(buffer.String()) != selfCheckOutput {
		t.Fatalf("%d %v %d", written, err, len(buffer.String()))
	}
	if written, err := buffer.Write([]byte("more")); err != nil || written != 4 || len(buffer.String()) != selfCheckOutput {
		t.Fatalf("after the limit: %d %v", written, err)
	}
}

// De bout en bout : le serveur offre la version importée, l'opérateur envoie
// l'ordre, l'agent télécharge, vérifie avec SA clé, s'éprouve avec le vrai
// code (le « binaire » est un script de test), bascule son lien et s'arrête
// avec ErrUpdated. Relancé en v0.0.2, il revient : l'ordre devient « fait ».
func TestRun_UpdatesItselfAndTheServerSeesItComeBack(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	operator := audit.WithActor(ctx, audit.Operator("lucas"))

	// La bibliothèque du serveur et son dépôt de colis.
	public, private, err := release.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	serverState, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverState.Close() })
	outbox, err := action.NewOutbox(serverState)
	if err != nil {
		t.Fatal(err)
	}
	b.actions.SetOutbox(outbox)
	library := release.NewLibrary(serverState, public)
	b.actions.SetReleases(library, "v0.0.2")
	script := "#!/bin/sh\necho 'opencloud v0.0.2 (test)'\n"
	if _, err := library.Import(releasetest.Write(t, private, "v0.0.2", map[string][]byte{release.BinaryName(runtime.GOARCH): []byte(script)})); err != nil {
		t.Fatal(err)
	}

	// L'agent est installé comme la doc le dit : bin/ et le lien current.
	if err := b.stateDir.MkdirAll("bin", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.WriteFile("bin/opencloud-v0.0.1", []byte("the running binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.Symlink("opencloud-v0.0.1", "bin/current"); err != nil {
		t.Fatal(err)
	}
	options := b.options("")
	options.Token, _, err = b.machines.CreateToken(ctx, "vps-paris-1", []machine.Capability{{Name: machine.CapabilityAgentUpdate}})
	if err != nil {
		t.Fatal(err)
	}
	options.Rights = []machine.Capability{{Name: machine.CapabilityAgentUpdate}}
	options.ReleaseKey, options.HasReleaseKey = public, true
	options.executable = func() (string, error) { return filepath.Join(b.stateDir.Name(), "bin", "opencloud-v0.0.1"), nil }

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(runCtx, options) }()
	online := b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return s.Online && !s.Agent.ReportedAt.IsZero() })

	offer, err := b.actions.OfferUpdate(operator, online.ID)
	if err != nil || !offer.Available || offer.Current != "v0.0.1" || offer.Target != "v0.0.2" {
		t.Fatalf("offer %+v %v", offer, err)
	}
	sent, err := b.actions.UpdateAgent(operator, online.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrUpdated) {
			t.Fatalf("Run returned %v, want ErrUpdated", err)
		}
	case <-time.After(benchTimeout):
		t.Fatal("the agent never restarted")
	}
	if target, err := b.stateDir.Readlink("bin/current"); err != nil || target != "opencloud-v0.0.2" {
		t.Fatalf("current points to %q (%v)", target, err)
	}
	// Tant que l'agent n'est pas revenu, l'ordre n'est pas fait.
	if run, err := b.db.GetActionRun(ctx, sent.ID); err != nil || run.State == action.StateSucceeded || run.State == action.StateFailed {
		t.Fatalf("before the agent came back: %+v %v", run, err)
	}

	// Relancé par systemd : la même identité, la nouvelle version.
	relaunch := options
	relaunch.Token, relaunch.Version = "", "v0.0.2"
	relaunch.executable = func() (string, error) { return filepath.Join(b.stateDir.Name(), "bin", "opencloud-v0.0.2"), nil }
	go func() { done <- Run(runCtx, relaunch) }()
	finished := b.waitForRun(t, sent.ID, action.StateSucceeded)
	if len(finished.Output) != 1 || finished.Output[0].Line != "reconnected as v0.0.2" {
		t.Fatalf("output %+v", finished.Output)
	}
	if offer, err := b.actions.OfferUpdate(operator, online.ID); err != nil || offer.Available || offer.Reason != action.OfferSameVersion {
		t.Fatalf("offer after the update %+v %v", offer, err)
	}
	cancel()
	<-done
}
