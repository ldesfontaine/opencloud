package server

import (
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/version"
)

// Les étapes de l'installation d'un agent, dans l'ordre où on les colle sur la
// machine. Chaque bloc se joue seul, d'un coup ; le front en dit le titre
// d'après l'identifiant.
const (
	stepStop    = "stop"
	stepBinary  = "binary"
	stepPrepare = "prepare"
	stepEnroll  = "enroll"
	stepUnit    = "unit"
	stepRights  = "rights"
)

// installTarget dit pour quelle machine le jeton est émis : local, la machine
// openCloud, dont le binaire est celui que le serveur a installé et non une
// copie dans /tmp ; reenroll, une machine qui a déjà un agent.
type installTarget struct {
	local    bool
	reenroll bool
}

// installSteps donne les étapes : l'arrêt de l'agent en place (un
// ré-enrôlement seulement), le binaire (sauf sur la machine openCloud, qui
// l'a déjà), la préparation, l'enrôlement, l'unité, et les dossiers que les
// droits autorisent (seulement quand un droit écrit hors de l'état). Le jeton
// n'est que dans l'enrôlement. L'unité vient après lui : elle relit
// l'identité que le premier lancement a écrite.
func installSteps(publicURL, token string, code lang.Code, capabilities []machine.Capability, target installTarget) []installStepJSON {
	var steps []installStepJSON
	if target.reenroll {
		steps = append(steps, installStepJSON{ID: stepStop, Command: installStop()})
	}
	if !target.local {
		steps = append(steps, installStepJSON{ID: stepBinary, Command: installBinary(publicURL)})
	}
	steps = append(steps,
		installStepJSON{ID: stepPrepare, Command: installPreparation(capabilities, version.Number(), target.local)},
		installStepJSON{ID: stepEnroll, Command: installCommand(publicURL, token, code, capabilities)},
		installStepJSON{ID: stepUnit, Command: installUnit()},
	)
	if rights := installRights(capabilities); rights != "" {
		steps = append(steps, installStepJSON{ID: stepRights, Command: rights})
	}
	return steps
}

// installStop arrête l'agent en place et oublie son identité : le jeton neuf
// en crée une autre, et la préparation se joue agent arrêté.
func installStop() string {
	return "systemctl stop " + brand.AgentUnit + "\nrm -f " + brand.AgentStateDir + "/" + brand.AgentIdentityFile
}

// serverHostPattern : ce qu'un nom ou une adresse de serveur peut porter pour
// entrer dans une commande sans guillemets.
var serverHostPattern = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)

// installBinary copie le binaire du serveur dans /tmp de la machine, d'où la
// préparation l'installe. Il se joue sur la machine, en root : l'adresse est
// celle de public_url. Le serveur n'offre pas son binaire par HTTP : la copie
// passe par SSH, avec les accès que l'opérateur a déjà. Hôte illisible : le
// repère ADRESSE_DU_SERVEUR reste à remplacer, la page ne devine rien.
func installBinary(publicURL string) string {
	host := "ADRESSE_DU_SERVEUR"
	if parsed, err := url.Parse(publicURL); err == nil && serverHostPattern.MatchString(parsed.Hostname()) {
		host = parsed.Hostname()
		if net.ParseIP(host) != nil && strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	const source = "/usr/local/bin/" + brand.Command
	const target = "/tmp/" + brand.Command
	return "scp root@" + host + ":" + source + " " + target + " && chmod 0755 " + target
}

// installUnit pose l'unité de l'agent et l'active. Le contenu est celui de
// machine.AgentUnitLines ; l'heredoc entre apostrophes ne laisse le shell rien
// interpréter.
func installUnit() string {
	lines := []string{"cat > " + machine.UnitFile + " <<'EOF'"}
	lines = append(lines, machine.AgentUnitLines()...)
	lines = append(lines,
		"EOF",
		"systemctl daemon-reload",
		"systemctl enable --now "+brand.AgentUnit,
	)
	return strings.Join(lines, "\n")
}

// rightsWritingOutsideState : les droits dont l'ordre écrit hors de l'état de
// l'agent, donc dans un dossier que l'unité doit autoriser (la racine des
// services, les sauvegardes reçues). files.write n'en est pas : ses dossiers
// sont préparés au nom du compte, l'unité n'y change rien.
var rightsWritingOutsideState = []string{machine.CapabilityDockerDeploy, machine.CapabilityDockerUpdate, machine.CapabilityBackupReceive}

// installRights autorise, dans un drop-in de l'unité, les dossiers où ces
// droits écrivent : sans lui, l'agent accepte l'ordre et ne peut pas écrire.
// Vide quand aucun droit n'écrit ailleurs que dans l'état. Les chemins
// viennent de writtenFolders, normalisés : ils passent entre apostrophes.
func installRights(capabilities []machine.Capability) string {
	var paths []string
	for _, folder := range writtenFolders(capabilities) {
		writesOutside := slices.ContainsFunc(folder.rights, func(right string) bool { return slices.Contains(rightsWritingOutsideState, right) })
		if writesOutside && !slices.Contains(paths, folder.path) {
			paths = append(paths, folder.path)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	return strings.Join([]string{
		"install -d -m 0755 " + machine.ProduceDropInDir,
		printfLines([]string{"[Service]", "ReadWritePaths=" + strings.Join(paths, " ")}) + " | install -m 0644 /dev/stdin " + machine.RightsDropInFile,
		"systemctl daemon-reload",
		"systemctl restart " + brand.AgentUnit,
	}, "\n")
}
