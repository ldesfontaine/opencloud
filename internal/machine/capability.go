package machine

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Les droits d'un agent : cochés à l'émission du jeton, écrits par la ligne
// d'installation dans le fichier de l'agent, rapportés par lui à chaque
// ouverture de flux, et appliqués par l'agent : un ordre dont le droit n'est
// pas dans son fichier est refusé (action.RightOf dit lequel). observe est
// toujours là. Les changer, c'est ré-enrôler avec d'autres droits.
// probe.lan est le seul droit qu'aucun ordre n'exige : c'est une borne de
// l'observation, que l'agent applique à ses propres sondes.
const (
	CapabilityObserve       = "observe"
	CapabilityProbeLAN      = "probe.lan"
	CapabilityAgentUpdate   = "agent.update"
	CapabilityDockerDeploy  = "docker.deploy"
	CapabilityDockerUpdate  = "docker.update"
	CapabilityBackupReceive = "backup.receive"
	CapabilityBackupProduce = "backup.produce"
	CapabilityFilesWrite    = "files.write"
)

// CapabilityNames rend la liste fermée, dans l'ordre où elle s'affiche :
// chaque droit a son libellé dans les catalogues.
func CapabilityNames() []string {
	return []string{
		CapabilityObserve, CapabilityProbeLAN, CapabilityAgentUpdate, CapabilityDockerDeploy,
		CapabilityDockerUpdate, CapabilityBackupReceive, CapabilityBackupProduce, CapabilityFilesWrite,
	}
}

// Capability est un droit et, pour un droit borné, les chemins qu'il couvre.
// Pour backup.produce, ce sont des noms de producteurs et non des chemins
// (produce.go).
type Capability struct {
	Name  string
	Paths []string
}

const (
	// La forme texte : les droits séparés par « , », un droit borné suivi de
	// « = » et de ses chemins séparés par « : ». Aucun de ces signes ni
	// espace dans un chemin : la ligne d'installation la porte sans
	// guillemets, l'en-tête de l'agent aussi.
	capabilitySeparator = ","
	scopeSeparator      = "="
	pathSeparator       = ":"
	maxCapabilityPaths  = 8
	maxCapabilityText   = 2048
)

var capabilityPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,254}$`)

var (
	ErrCapabilityUnknown = refusal.Invalid("machine.capability_unknown", "unknown agent capability")
	ErrCapabilityPath    = refusal.Invalid("machine.capability_path", "capability path must be absolute, clean, and made of letters, digits, dot, dash, underscore")
	ErrCapabilityScope   = refusal.Invalid("machine.capability_scope", "backup.receive takes one path, files.write one to eight, backup.produce one to four producers, the others none")
)

// NormalizeCapabilities vérifie une liste de droits, ajoute observe, retire
// les doublons et la range dans l'ordre de CapabilityNames : deux listes
// égales s'écrivent pareil.
func NormalizeCapabilities(capabilities []Capability) ([]Capability, error) {
	byName := map[string]Capability{CapabilityObserve: {Name: CapabilityObserve}}
	for _, capability := range capabilities {
		if !slices.Contains(CapabilityNames(), capability.Name) {
			return nil, ErrCapabilityUnknown
		}
		paths, err := normalizePaths(capability)
		if err != nil {
			return nil, err
		}
		byName[capability.Name] = Capability{Name: capability.Name, Paths: paths}
	}
	normalized := make([]Capability, 0, len(byName))
	for _, name := range CapabilityNames() {
		if capability, ok := byName[name]; ok {
			normalized = append(normalized, capability)
		}
	}
	return normalized, nil
}

// normalizePaths juge la portée d'un droit : un chemin pour backup.receive, un
// à huit pour files.write, des producteurs pour backup.produce, rien pour les
// autres. Elle ressort triée, sans doublon.
func normalizePaths(capability Capability) ([]string, error) {
	if capability.Name == CapabilityBackupProduce {
		return normalizeProducers(capability)
	}
	paths := slices.Clone(capability.Paths)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	for _, candidate := range paths {
		if !ValidCapabilityPath(candidate) {
			return nil, ErrCapabilityPath
		}
	}
	switch capability.Name {
	case CapabilityBackupReceive:
		if len(paths) != 1 {
			return nil, ErrCapabilityScope
		}
	case CapabilityFilesWrite:
		if len(paths) == 0 || len(paths) > maxCapabilityPaths {
			return nil, ErrCapabilityScope
		}
	default:
		if len(paths) != 0 {
			return nil, ErrCapabilityScope
		}
		return nil, nil
	}
	return paths, nil
}

// ValidCapabilityPath : un chemin absolu, déjà propre, jamais la racine :
// « / » voudrait dire toute la machine.
func ValidCapabilityPath(candidate string) bool {
	return capabilityPathPattern.MatchString(candidate) && path.Clean(candidate) == candidate
}

// CoversPath dit si un droit borné couvre ce chemin : le chemin du droit, ou
// un dossier dessous. Le préfixe se compare avec son séparateur,
// « /srv/backups2 » n'est pas sous « /srv/backups ». Le chemin demandé doit
// être propre : « /srv/backups/../etc » ne l'est pas.
func CoversPath(capabilities []Capability, name, candidate string) bool {
	_, covered := CoveringPath(capabilities, name, candidate)
	return covered
}

// CoveringPath rend le chemin du droit qui couvre candidate (le plus long
// s'il y en a plusieurs) : l'agent ouvre sa racine là, pour qu'un lien posé
// dessous ne fasse pas sortir l'écriture du droit.
func CoveringPath(capabilities []Capability, name, candidate string) (string, bool) {
	// La portée de backup.produce est une liste de producteurs : aucun chemin
	// ne s'y compare (CoversProducer).
	if name == CapabilityBackupProduce || !ValidCapabilityPath(candidate) {
		return "", false
	}
	covering := ""
	for _, capability := range capabilities {
		if capability.Name != name {
			continue
		}
		for _, granted := range capability.Paths {
			if (candidate == granted || strings.HasPrefix(candidate, granted+"/")) && len(granted) > len(covering) {
				covering = granted
			}
		}
	}
	return covering, covering != ""
}

// FormatCapabilities écrit une liste déjà normalisée sous sa forme texte.
func FormatCapabilities(capabilities []Capability) string {
	items := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		items = append(items, formatCapability(capability))
	}
	return strings.Join(items, capabilitySeparator)
}

func formatCapability(capability Capability) string {
	if len(capability.Paths) == 0 {
		return capability.Name
	}
	return capability.Name + scopeSeparator + strings.Join(capability.Paths, pathSeparator)
}

// ParseCapabilities lit la forme texte et la normalise. Un texte vide vaut
// observe seul.
func ParseCapabilities(text string) ([]Capability, error) {
	if len(text) > maxCapabilityText {
		return nil, ErrCapabilityScope
	}
	var capabilities []Capability
	for item := range strings.SplitSeq(text, capabilitySeparator) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, scope, hasScope := strings.Cut(item, scopeSeparator)
		capability := Capability{Name: name}
		if hasScope {
			capability.Paths = strings.Split(scope, pathSeparator)
		}
		capabilities = append(capabilities, capability)
	}
	return NormalizeCapabilities(capabilities)
}

// HasCapability dit si la liste porte ce droit. Les chemins d'un droit borné
// se jugent avec l'ordre qui les nomme, pas ici.
func HasCapability(capabilities []Capability, name string) bool {
	return slices.ContainsFunc(capabilities, func(capability Capability) bool { return capability.Name == name })
}

// SameCapabilities dit si deux listes normalisées disent la même chose.
func SameCapabilities(first, second []Capability) bool {
	return FormatCapabilities(first) == FormatCapabilities(second)
}
