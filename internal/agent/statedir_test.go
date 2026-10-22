package agent

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// lockedBuffer reçoit le journal de l'agent, écrit depuis ses goroutines.
type lockedBuffer struct {
	mu   sync.Mutex
	text strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

// node est un dossier de la chaîne factice : son propriétaire et son mode.
type node struct {
	uid  uint32
	mode fs.FileMode
}

// fakeStat rend un stat sur une chaîne qu'on écrit à la main : les tests de
// la règle ne dépendent ni du compte qui les lance ni du disque.
func fakeStat(chain map[string]node) func(string) (uint32, fs.FileMode, error) {
	return func(path string) (uint32, fs.FileMode, error) {
		found, ok := chain[path]
		if !ok {
			return 0, 0, errors.New("stat " + path + ": no such file or directory")
		}
		return found.uid, found.mode, nil
	}
}

const (
	dir0755 = fs.ModeDir | 0o755
	dir0700 = fs.ModeDir | 0o700
)

// safeChain est la disposition voulue : tout à root, l'état en 0700.
func safeChain() map[string]node {
	return map[string]node{
		"/":                        {0, dir0755},
		"/var":                     {0, dir0755},
		"/var/lib":                 {0, dir0755},
		"/var/lib/opencloud-agent": {0, dir0700},
		"/var/lib/opencloud":       {999, dir0755},
		"/var/lib/opencloud/agent": {0, dir0700},
		"/tmp":                     {0, fs.ModeDir | fs.ModeSticky | 0o777},
		"/tmp/state":               {1000, dir0700},
		"/home":                    {0, dir0755},
		"/home/agent":              {1000, dir0755},
		"/home/agent/state":        {1000, dir0700},
	}
}

func requireUnsafe(t *testing.T, err error, wantPath string, wantUID uint32) {
	t.Helper()
	var unsafe *UnsafeStateError
	if !errors.As(err, &unsafe) {
		t.Fatalf("want an UnsafeStateError on %s, got %v", wantPath, err)
	}
	if unsafe.Path != wantPath || unsafe.UID != wantUID {
		t.Fatalf("refused %s (uid %d), want %s (uid %d)", unsafe.Path, unsafe.UID, wantPath, wantUID)
	}
}

func TestCheckChain_RootOwnedStateUnderRootDirsIsAccepted(t *testing.T) {
	if err := checkChain("/var/lib/opencloud-agent", fakeStat(safeChain()), 0); err != nil {
		t.Fatal(err)
	}
}

// Le cas qui a fait le chantier : l'état est à root, mais le dossier du
// serveur au-dessus appartient à son compte, qui peut y renommer un dossier.
func TestCheckChain_ParentOwnedByAnotherAccountIsRefusedAndNamed(t *testing.T) {
	err := checkChain("/var/lib/opencloud/agent", fakeStat(safeChain()), 0)
	requireUnsafe(t, err, "/var/lib/opencloud", 999)
	if message := err.Error(); !strings.Contains(message, "/var/lib/opencloud is owned by uid 999") {
		t.Fatalf("message %q", message)
	}
}

func TestCheckChain_ParentWritableByGroupOrOthersIsRefused(t *testing.T) {
	for name, mode := range map[string]fs.FileMode{
		"group writable":  dir0755 | 0o020,
		"others writable": dir0755 | 0o002,
		"world writable":  fs.ModeDir | 0o777,
	} {
		t.Run(name, func(t *testing.T) {
			chain := safeChain()
			chain["/var/lib"] = node{0, mode}
			requireUnsafe(t, checkChain("/var/lib/opencloud-agent", fakeStat(chain), 0), "/var/lib", 0)
		})
	}
}

func TestCheckChain_StickyParentIsAccepted_ButNotTheStateDirItself(t *testing.T) {
	// /tmp en 1777 : personne n'y renomme le dossier d'un autre.
	if err := checkChain("/tmp/state", fakeStat(safeChain()), 1000); err != nil {
		t.Fatalf("sticky parent: %v", err)
	}
	// Un dossier d'état 1777 laisserait d'autres comptes y poser des fichiers.
	chain := safeChain()
	chain["/tmp/state"] = node{1000, fs.ModeDir | fs.ModeSticky | 0o777}
	requireUnsafe(t, checkChain("/tmp/state", fakeStat(chain), 1000), "/tmp/state", 1000)
}

// Le bit collant ne remplace pas le propriétaire : un /tmp à un autre compte
// est refusé.
func TestCheckChain_StickyParentOfAnotherAccountIsStillRefused(t *testing.T) {
	chain := safeChain()
	chain["/tmp"] = node{999, fs.ModeDir | fs.ModeSticky | 0o777}
	requireUnsafe(t, checkChain("/tmp/state", fakeStat(chain), 1000), "/tmp", 999)
}

func TestCheckChain_StateOwnedByTheAgentAccountUnderRootDirsIsAccepted(t *testing.T) {
	if err := checkChain("/home/agent/state", fakeStat(safeChain()), 1000); err != nil {
		t.Fatal(err)
	}
	// Le même état, vu par un agent d'un autre compte : refusé.
	requireUnsafe(t, checkChain("/home/agent/state", fakeStat(safeChain()), 1001), "/home/agent/state", 1000)
}

func TestCheckChain_StateDirWithGroupAccessIsRefused(t *testing.T) {
	chain := safeChain()
	chain["/var/lib/opencloud-agent"] = node{0, fs.ModeDir | 0o770}
	requireUnsafe(t, checkChain("/var/lib/opencloud-agent", fakeStat(chain), 0), "/var/lib/opencloud-agent", 0)
}

func TestCheckChain_StatFailureOnAParentIsARefusal(t *testing.T) {
	chain := safeChain()
	delete(chain, "/var/lib")
	err := checkChain("/var/lib/opencloud-agent", fakeStat(chain), 0)
	var unsafe *UnsafeStateError
	if err == nil || errors.As(err, &unsafe) || !strings.Contains(err.Error(), "/var/lib") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckChain_RelativeOrUncleanPathIsRefused(t *testing.T) {
	for _, path := range []string{"var/lib/opencloud-agent", "./state", "/var/lib/../lib/opencloud-agent", "/var/lib/opencloud-agent/"} {
		if err := checkChain(path, fakeStat(safeChain()), 0); err == nil {
			t.Fatalf("%q accepted", path)
		}
	}
}

// Un lien symbolique de la chaîne donnée a toujours le mode 0777 : seul son
// propriétaire compte ici, sa cible est jugée par la chaîne résolue.
func TestCheckChain_SymlinkOnTheChainIsJudgedByItsOwnerOnly(t *testing.T) {
	chain := safeChain()
	chain["/var/run"] = node{0, fs.ModeSymlink | 0o777}
	if err := checkChain("/var/run", fakeStat(chain), 0); err != nil {
		t.Fatalf("root-owned link: %v", err)
	}
	chain["/var/run"] = node{999, fs.ModeSymlink | 0o777}
	requireUnsafe(t, checkChain("/var/run", fakeStat(chain), 0), "/var/run", 999)
}

// Sur disque, sans root : des dossiers du compte qui lance le test, dont on
// joue seulement le mode. Ce qui est au-dessus de t.TempDir() (le /tmp de la
// machine) ne regarde pas ces tests : ils ne jugent que l'endroit refusé.
func mkdir(t *testing.T, path string, mode fs.FileMode) string {
	t.Helper()
	if err := os.Mkdir(path, mode); err != nil {
		t.Fatal(err)
	}
	// L'umask a pu retirer des bits : le mode se pose après coup.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// base rend un dossier de test 0700, chemin résolu.
func base(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(resolved, 0o700); err != nil {
		t.Fatal(err)
	}
	return resolved
}

// refusedAt rend le chemin que l'erreur nomme, "" sans erreur.
func refusedAt(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var unsafe *UnsafeStateError
	if !errors.As(err, &unsafe) {
		t.Fatalf("not an UnsafeStateError: %v", err)
	}
	return unsafe.Path
}

// insideBase : l'erreur, s'il y en a une, ne vient pas d'un dossier fait ici.
func insideBase(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func TestCheckStateDir_OnDisk(t *testing.T) {
	root := base(t)
	safe := mkdir(t, filepath.Join(root, "safe"), 0o700)
	if refused := refusedAt(t, CheckStateDir(safe)); insideBase(refused, root) {
		t.Fatalf("a private dir under a private dir is refused at %s", refused)
	}
	open := mkdir(t, filepath.Join(root, "open"), 0o777)
	inOpen := mkdir(t, filepath.Join(open, "state"), 0o700)
	if refused := refusedAt(t, CheckStateDir(inOpen)); refused != open {
		t.Fatalf("state under a 0777 dir: refused at %q, want %s", refused, open)
	}
	shared := mkdir(t, filepath.Join(root, "shared"), 0o770)
	if refused := refusedAt(t, CheckStateDir(shared)); refused != shared {
		t.Fatalf("state with group write: refused at %q, want %s", refused, shared)
	}
	if err := CheckStateDir(filepath.Join(root, "missing")); err == nil {
		t.Fatal("a missing state dir is not safe")
	}
}

// Un chemin relatif se juge comme l'absolu qu'il désigne.
func TestCheckStateDir_RelativePathIsMadeAbsolute(t *testing.T) {
	root := base(t)
	open := mkdir(t, filepath.Join(root, "open"), 0o777)
	mkdir(t, filepath.Join(open, "state"), 0o700)
	mkdir(t, filepath.Join(root, "safe"), 0o700)
	t.Chdir(root)
	if refused := refusedAt(t, CheckStateDir("open/state")); refused != open {
		t.Fatalf("refused at %q, want %s", refused, open)
	}
	if refused := refusedAt(t, CheckStateDir("safe")); insideBase(refused, root) {
		t.Fatalf("refused at %s", refused)
	}
}

// Le chemin donné et le chemin résolu sont jugés tous les deux.
func TestCheckStateDir_ResolvedPathIsJudgedToo(t *testing.T) {
	root := base(t)
	private := mkdir(t, filepath.Join(root, "private"), 0o700)
	open := mkdir(t, filepath.Join(root, "open"), 0o777)

	// Donné : un lien rangé dans un dossier 0777, vers un dossier privé. La
	// cible est sûre, mais celui qui peut écrire dans open/ remplace le lien.
	if err := os.Symlink(private, filepath.Join(open, "state")); err != nil {
		t.Fatal(err)
	}
	if refused := refusedAt(t, CheckStateDir(filepath.Join(open, "state"))); refused != open {
		t.Fatalf("given path: refused at %q, want %s", refused, open)
	}

	// Résolu : un lien sûr (dans un dossier privé) vers un état rangé dans un
	// dossier 0777. Le chemin donné est irréprochable, la cible ne l'est pas.
	inOpen := mkdir(t, filepath.Join(open, "real"), 0o700)
	link := filepath.Join(root, "link")
	if err := os.Symlink(inOpen, link); err != nil {
		t.Fatal(err)
	}
	if refused := refusedAt(t, CheckStateDir(link)); refused != open {
		t.Fatalf("resolved path: refused at %q, want %s", refused, open)
	}
}

// Lancé avec un état mal gardé, l'agent rapporte observe seul et refuse un
// ordre qui écrit, mais n'abîme pas le fichier : l'état corrigé, un
// redémarrage rend les droits.
func TestRun_UnsafeStateKeepsObserveOnly(t *testing.T) {
	b := newBench(t)
	rights, err := machine.ParseCapabilities("docker.deploy")
	if err != nil {
		t.Fatal(err)
	}
	token, _, _ := b.machines.CreateToken(context.Background(), "vps-brest-1", rights)
	enrolling := b.options(token)
	enrolling.Rights = rights
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, enrolling) }()
	granted := b.waitFor(t, "vps-brest-1", func(s machine.Status) bool {
		return s.Online && machine.HasCapability(s.Agent.Capabilities, machine.CapabilityDockerDeploy)
	})
	cancel()
	<-done
	b.waitFor(t, "vps-brest-1", func(s machine.Status) bool { return !s.Online })

	// Redémarré sur le même état, que la commande a jugé mal gardé.
	var logs lockedBuffer
	narrowed := b.options("")
	narrowed.UnsafeState = &UnsafeStateError{Path: "/var/lib/opencloud", UID: 999, Mode: dir0755}
	narrowed.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- Run(ctx, narrowed) }()
	reported := b.waitFor(t, "vps-brest-1", func(s machine.Status) bool {
		return s.Online && !s.Agent.ReportedAt.IsZero() && !machine.HasCapability(s.Agent.Capabilities, machine.CapabilityDockerDeploy)
	})
	if machine.FormatCapabilities(reported.Agent.Capabilities) != "observe" || !reported.Agent.CapabilitiesDiffer() {
		t.Fatalf("reported %+v", reported.Agent)
	}
	if !strings.Contains(logs.String(), "state dir is not safe, writing rights are off") || !strings.Contains(logs.String(), "/var/lib/opencloud is owned by uid 999") {
		t.Fatalf("journal %q", logs.String())
	}
	// Le serveur, qui sait le droit absent, ne pousse pas l'ordre.
	operator := audit.WithActor(context.Background(), audit.Operator("lucas"))
	if _, err := b.actions.Send(operator, granted.ID, agentwire.KindDockerDeploy, nil); !errors.Is(err, action.ErrRightMissing) {
		t.Fatalf("deploy sent anyway: %v", err)
	}
	cancel()
	<-done

	identity, err := LoadIdentity(b.stateDir)
	if err != nil || strings.Join(identity.Rights, ",") != "observe,docker.deploy" {
		t.Fatalf("rights on disk %+v %v", identity.Rights, err)
	}
}

// Côté agent : même si un ordre signé arrivait, il est refusé right_missing,
// et le rang qu'il écrit ne touche pas aux droits du fichier.
func TestOrderRunner_UnsafeStateRefusesWritingOrdersAndKeepsRightsOnDisk(t *testing.T) {
	b := newOrderBenchWithRights(t, []string{"observe", "docker.deploy"})
	b.runner.identity.narrowed = true

	deploy := order(1)
	deploy.Kind = agentwire.KindDockerDeploy
	result := b.send(t, deploy, b.private)
	if result.State != agentwire.ActionRefused || result.Reason != agentwire.RightMissing(machine.CapabilityDockerDeploy) {
		t.Fatalf("deploy %+v", result)
	}
	// observe reste : un ping passe, et son rang est réécrit dans le fichier.
	if ping := b.send(t, order(2), b.private); ping.State != agentwire.ActionSucceeded {
		t.Fatalf("ping %+v", ping)
	}
	saved, err := LoadIdentity(b.stateDir)
	if err != nil || saved.LastOrderSeq != 2 || strings.Join(saved.Rights, ",") != "observe,docker.deploy" {
		t.Fatalf("saved %+v %v", saved, err)
	}
	if !b.runner.identity.narrowed || len(b.runner.identity.Capabilities()) != 1 {
		t.Fatalf("narrowing lost after a write: %+v", b.runner.identity.Capabilities())
	}
}
