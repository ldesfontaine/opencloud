package agent

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Un droit coché n'est pas un droit exerçable : le compte sous lequel l'agent
// tourne peut ne pas ouvrir la socket de Docker, ne pas posséder un dossier.
// L'agent le juge deux fois avec les mêmes questions : avant d'ouvrir le flux,
// pour le dire au serveur (judgeRights), et au moment de l'ordre, pour le
// refuser avant d'écrire quoi que ce soit, avec la même raison.

const (
	dockerDialTimeout = 2 * time.Second
	// sudo -n -l répond sans rien exécuter ni demander : un délai large pour une
	// machine dont sudo interroge un annuaire.
	sudoListTimeout = 10 * time.Second
	// access(2) : écrire dans un dossier et le traverser. Créer dessous exige
	// les deux ; syscall n'en nomme pas les constantes.
	accessWrite  = 0x2
	accessSearch = 0x1
)

// hostChecks est ce que l'agent demande à sa machine pour juger un droit.
// Chaque réponse est vide quand tout va bien, sinon une raison de refus
// (agentwire.Reason*). Les tests les remplacent.
type hostChecks struct {
	// docker : la socket s'ouvre et /usr/bin/docker est là.
	docker func(ctx context.Context) string
	// writable : le dossier existe et s'écrit, ou se crée sous le plus proche
	// parent qui existe.
	writable func(path string) string
	// agentUpdate : l'agent tourne depuis <état>/bin/ et ce dossier s'écrit.
	agentUpdate func() string
	// lookPath : un programme du PATH, ou un chemin absolu, que le compte peut
	// lancer (exec.LookPath).
	lookPath func(name string) (string, error)
	// sudoList : « sudo -n -l <programme> », qui répond sans rien exécuter ni
	// demander de mot de passe : la règle de sudo laisse le compte lancer ce
	// programme. Une erreur : elle ne le laisse pas.
	sudoList func(ctx context.Context, program string) error
}

// newHostChecks branche les vérifications sur la vraie machine.
func newHostChecks(socket string, updater *selfUpdater) hostChecks {
	return hostChecks{
		docker:      func(ctx context.Context) string { return dockerReason(ctx, dockerBinary, socket) },
		writable:    writableOrCreatable,
		agentUpdate: func() string { return updater.unavailableReason(writableOrCreatable) },
		lookPath:    exec.LookPath,
		sudoList:    sudoListsProgram,
	}
}

// allowAll est des vérifications qui ne refusent jamais : un banc sans compte
// à juger.
func allowAll() hostChecks {
	return hostChecks{
		docker:      func(context.Context) string { return "" },
		writable:    func(string) string { return "" },
		agentUpdate: func() string { return "" },
		lookPath:    func(name string) (string, error) { return name, nil },
		sudoList:    func(context.Context, string) error { return nil },
	}
}

// sudoListsProgram demande à sudo si la règle du compte laisse lancer ce
// programme. Le résultat se lit au code de sortie ; la sortie est jetée.
func sudoListsProgram(ctx context.Context, program string) error {
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sudoListTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, sudo, "-n", "-l", program) // #nosec G204 -- bounded: sudo est celui du PATH de l'unité, program une constante de brand.
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	return command.Run()
}

// dockerReason dit si le compte de l'agent peut parler à Docker : docker_missing
// sans programme ou sans socket, docker_denied si la socket lui est fermée.
// Docker arrêté (la socket est là, le démon ne répond pas) n'est pas un droit
// indisponible : compose up le dira.
func dockerReason(ctx context.Context, binary, socket string) string {
	if _, err := os.Stat(binary); err != nil {
		return agentwire.ReasonDockerMissing
	}
	return dockerReasonOf(dialDocker(ctx, socket))
}

func dialDocker(ctx context.Context, socket string) error {
	dialer := net.Dialer{Timeout: dockerDialTimeout}
	connection, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	return connection.Close()
}

// dockerReasonOf traduit l'erreur d'une ouverture de socket. Séparée de
// l'ouverture : un test lui donne un EACCES fabriqué, sans dépendre du compte
// qui le lance.
func dockerReasonOf(err error) string {
	if err == nil {
		return ""
	}
	classified := dockerapi.ClassifyDial(err)
	switch {
	case errors.Is(classified, dockerapi.ErrSocketDenied):
		return agentwire.ReasonDockerDenied
	case errors.Is(classified, dockerapi.ErrNoSocket):
		return agentwire.ReasonDockerMissing
	}
	return ""
}

// writableOrCreatable juge un dossier sur la machine, avec le compte du
// processus. access juge l'uid réel ; sous systemd et sous « sudo -u », réel et
// effectif sont le même. C'est un pré-contrôle lisible : l'écriture reste
// confinée par os.Root et peut encore échouer (path_unwritable).
func writableOrCreatable(path string) string {
	return judgeWritable(path, os.Stat, accessWritable)
}

func accessWritable(dir string) error {
	return syscall.Access(dir, accessWrite|accessSearch) // #nosec G703 -- un chemin de droit, lu dans le fichier de l'agent ; access ne lit ni n'écrit rien.
}

// judgeWritable remonte jusqu'au plus proche chemin qui existe, puis demande à
// access : EROFS (l'unité ne l'ouvre pas, ReadWritePaths) est path_unwritable,
// EACCES et EPERM (le compte n'a pas ce dossier) path_denied. Les liens se
// suivent, comme os.OpenRoot suit le chemin du droit lui-même : seul ce qui
// est dessous est confiné, et l'écriture le confine encore.
func judgeWritable(path string, stat func(string) (fs.FileInfo, error), access func(string) error) string {
	existing := path
	for {
		info, err := stat(existing)
		switch {
		case err == nil && !info.IsDir():
			return agentwire.ReasonPathUnwritable
		case err == nil:
			return writableReason(access(existing))
		case errors.Is(err, fs.ErrNotExist):
			parent := filepath.Dir(existing)
			if parent == existing {
				return agentwire.ReasonPathUnwritable
			}
			existing = parent
		case errors.Is(err, fs.ErrPermission):
			// Un dossier au-dessus ne se traverse pas.
			return agentwire.ReasonPathDenied
		default:
			return agentwire.ReasonPathUnwritable
		}
	}
}

func writableReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, syscall.EROFS):
		return agentwire.ReasonPathUnwritable
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return agentwire.ReasonPathDenied
	}
	return agentwire.ReasonPathUnwritable
}

// judgeRights dit, pour chaque droit du fichier de l'agent, pourquoi son compte
// ne peut pas l'exercer sur la machine ; un droit réalisable n'y figure pas.
// observe n'est pas jugé : l'état de Docker se lit déjà par le moteur. Un
// état mal gardé (narrowed) ne laisse que observe : rien à juger.
func judgeRights(ctx context.Context, identity Identity, checks hostChecks) map[string]string {
	unavailable := map[string]string{}
	note := func(right, reason string) {
		if reason != "" {
			unavailable[right] = reason
		}
	}
	for _, capability := range identity.Capabilities() {
		switch capability.Name {
		case machine.CapabilityAgentUpdate:
			note(capability.Name, checks.agentUpdate())
		case machine.CapabilityDockerDeploy, machine.CapabilityDockerUpdate:
			note(capability.Name, dockerRightReason(ctx, checks, identity.Root()))
		case machine.CapabilityBackupReceive, machine.CapabilityFilesWrite:
			note(capability.Name, firstPathReason(checks, capability.Paths))
		case machine.CapabilityBackupProduce:
			note(capability.Name, firstProducerReason(ctx, checks, capability.Paths))
		}
	}
	return unavailable
}

// dockerRightReason : la socket d'abord, puis la racine des services.
func dockerRightReason(ctx context.Context, checks hostChecks, root string) string {
	if reason := checks.docker(ctx); reason != "" {
		return reason
	}
	return checks.writable(root)
}

// firstPathReason rend la raison du premier dossier du droit qui ne s'écrit pas.
func firstPathReason(checks hostChecks, paths []string) string {
	for _, path := range paths {
		if reason := checks.writable(path); reason != "" {
			return reason
		}
	}
	return ""
}

// firstProducerReason rend la raison du premier producteur du droit que la
// machine ne sait pas lancer.
func firstProducerReason(ctx context.Context, checks hostChecks, producers []string) string {
	for _, producer := range producers {
		if reason := producerReason(ctx, checks, producer); reason != "" {
			return reason
		}
	}
	return ""
}

// producerReason dit pourquoi le compte ne peut pas lancer ce producteur : pas
// de sudo, un programme qui manque (le script de root que la préparation pose,
// ou vzdump), ou une règle sudo qui ne laisse pas le compte lancer le script.
// Le programme avant la règle : sans le script, sudo -l répondrait non pour
// une raison que la préparation seule explique. Un producteur que l'agent ne
// sait pas lancer manque : le fichier de l'agent n'en porte pas
// (ParseCapabilities), mais un oubli ici ne doit pas le dire disponible.
func producerReason(ctx context.Context, checks hostChecks, producer string) string {
	if producer != machine.ProducerProxmoxVzdump {
		return agentwire.ReasonProducerMissing
	}
	if _, err := checks.lookPath("sudo"); err != nil {
		return agentwire.ReasonSudoMissing
	}
	for _, program := range []string{brand.VzdumpWrapperPath, machine.VzdumpBinary} {
		if _, err := checks.lookPath(program); err != nil {
			return agentwire.ReasonProducerMissing
		}
	}
	if err := checks.sudoList(ctx, brand.VzdumpWrapperPath); err != nil {
		return agentwire.ReasonSudoDenied
	}
	return ""
}
