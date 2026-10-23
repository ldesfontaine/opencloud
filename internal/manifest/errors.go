package manifest

import (
	"sort"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Les clés du catalogue des refus de lecture. Le front et la commande les
// traduisent : le serveur ne dit jamais un texte.
const (
	KeyYAMLInvalid        = "manifest.yaml_invalid"
	KeyDocumentMultiple   = "manifest.document_multiple"
	KeyFileTooLarge       = "manifest.file_too_large"
	KeyAliasNotAllowed    = "manifest.alias_not_allowed"
	KeyVersionMissing     = "manifest.version_missing"
	KeyVersionUnsupported = "manifest.version_unsupported"
	KeyUnknownKey         = "manifest.unknown_key"
	KeyDuplicateKey       = "manifest.duplicate_key"
	KeySectionUnsupported = "manifest.section_not_supported"
	KeyTypeInvalid        = "manifest.type_invalid"
	KeyDurationInvalid    = "manifest.duration_invalid"
	KeySecretInFile       = "manifest.secret_in_file"
	KeySecretInvalid      = "manifest.secret_invalid"
	KeyIgnoredInvalid     = "manifest.ignored_invalid"
	KeyZeroValue          = "manifest.zero_value"
)

// Les clés des refus du plan : ce que le fichier demande n'a pas de sens
// face à l'annuaire. Un refus d'un composant du domaine (nom invalide,
// fournitures sans infrastructure…) garde, lui, la clé de son domaine :
// directory.*, project.*, probe.*, job.*.
const (
	KeyIDDuplicate     = "manifest.id_duplicate"
	KeyIDUnknown       = "manifest.id_unknown"
	KeyNameAmbiguous   = "manifest.name_ambiguous"
	KeyNameDuplicate   = "manifest.name_duplicate"
	KeyMachineUnknown  = "manifest.machine_unknown"
	KeyServiceUnknown  = "manifest.service_unknown"
	KeyProjectUnknown  = "manifest.project_unknown"
	KeyReferenceBroken = "manifest.reference_invalid"
	KeyPruneBlocked    = "manifest.prune_blocked"
	KeyPruneStatusPage = "manifest.prune_status_page"
	KeySwapRefused     = "manifest.swap_refused"
	KeyKeyFromPruned   = "manifest.key_from_pruned"
	KeyEntryInvalid    = "manifest.entry_invalid"
)

// Les clés du catalogue que le journal et les réponses de refus portent :
// ce ne sont pas des refus de lecture ou de plan, elles disent pourquoi une
// application ne s'est pas faite ou s'est arrêtée.
const (
	KeyPlanChanged    = "manifest.plan_changed"
	KeyBusy           = "manifest.busy"
	KeyNotApplicable  = "manifest.not_applicable"
	KeyNothingToApply = "manifest.nothing_to_apply"
	KeyInterrupted    = "manifest.interrupted"
	KeyTimeout        = "manifest.timeout"
	KeyApplyFailed    = "manifest.apply_failed"
	// KeyRequestInvalid : la requête n'est pas un fichier à juger (corps
	// illisible, trop gros).
	KeyRequestInvalid = "manifest.request_invalid"
)

// ApplyKeys liste les clés qu'une application peut laisser dans le journal
// ou rendre en refus.
func ApplyKeys() []string {
	return []string{KeyPlanChanged, KeyBusy, KeyNotApplicable, KeyNothingToApply, KeyInterrupted, KeyTimeout, KeyApplyFailed, KeyRequestInvalid}
}

// KeyExportTooLarge répète la clé de ErrExportTooLarge, pour qui la cite. Ce
// n'est pas un refus de lecture : elle n'est pas dans ProblemKeys.
const KeyExportTooLarge = "manifest.export_too_large"

// ErrExportTooLarge : écrire ce fichier, c'est en produire un que la lecture
// refuserait. Mieux vaut le dire à l'export qu'au retour.
var ErrExportTooLarge = refusal.Invalid("manifest.export_too_large", "the directory is too large for a manifest file")

// Problem est un refus du fichier, à sa ligne : de lecture, ou du plan. Value
// est ce que le message cite (la clé fautive, la durée mal écrite), Path
// l'entrée visée quand le message la nomme. Une ligne à 0 est un refus qui
// ne tient à aucune entrée du fichier.
type Problem struct {
	Key    string
	Line   int
	Column int
	Path   string
	Value  string
}

// arguments dit ce que le texte du catalogue attend, dans l'ordre.
type arguments int

const (
	lineAndValue arguments = iota
	lineOnly
	valueOnly
	pathAndValue
)

var problemArguments = map[string]arguments{
	KeyYAMLInvalid:        lineAndValue,
	KeyDocumentMultiple:   lineOnly,
	KeyFileTooLarge:       valueOnly,
	KeyAliasNotAllowed:    lineOnly,
	KeyVersionMissing:     lineOnly,
	KeyVersionUnsupported: lineAndValue,
	KeyUnknownKey:         lineAndValue,
	KeyDuplicateKey:       lineAndValue,
	KeySectionUnsupported: lineAndValue,
	KeyTypeInvalid:        lineAndValue,
	KeyDurationInvalid:    lineAndValue,
	KeySecretInFile:       lineOnly,
	KeySecretInvalid:      lineOnly,
	KeyIgnoredInvalid:     lineOnly,
	KeyZeroValue:          lineAndValue,
	KeyIDDuplicate:        lineAndValue,
	KeyIDUnknown:          lineOnly,
	KeyNameAmbiguous:      lineOnly,
	KeyNameDuplicate:      lineAndValue,
	KeyMachineUnknown:     lineAndValue,
	KeyServiceUnknown:     lineAndValue,
	KeyProjectUnknown:     lineAndValue,
	KeyReferenceBroken:    lineAndValue,
	KeyPruneBlocked:       pathAndValue,
	KeyPruneStatusPage:    pathAndValue,
	KeySwapRefused:        valueOnly,
	KeyKeyFromPruned:      pathAndValue,
	KeyEntryInvalid:       lineAndValue,
}

// ProblemKeys liste les clés propres au fichier qu'une lecture ou un plan
// peut rendre, dans l'ordre. Les clés des domaines n'y sont pas.
func ProblemKeys() []string {
	keys := make([]string, 0, len(problemArguments))
	for key := range problemArguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// IsDomainRefusal dit que le refus est celui d'un composant du domaine, dont
// le texte du catalogue ne cite ni ligne ni valeur : on le fait précéder de
// la ligne et de l'entrée.
func (p Problem) IsDomainRefusal() bool {
	_, isOwn := problemArguments[p.Key]
	return !isOwn
}

// Arguments rend ce que le texte du catalogue de la clé met dans ses
// « %d » et ses « %s » : la ligne, la valeur citée, ou l'un des deux. Pour
// un refus de domaine, rien : voir IsDomainRefusal.
func (p Problem) Arguments() []any {
	if p.IsDomainRefusal() {
		return nil
	}
	switch problemArguments[p.Key] {
	case lineOnly:
		return []any{p.Line}
	case valueOnly:
		return []any{p.Value}
	case pathAndValue:
		return []any{p.Path, p.Value}
	default:
		return []any{p.Line, p.Value}
	}
}

// Sections que le modèle prévoit sans que le fichier les porte encore : les
// lire sans rien en faire ferait croire qu'elles sont appliquées.
var (
	unsupportedRoot      = []string{"status_pages", "channels", "backup_destinations", "backups", "settings"}
	unsupportedService   = []string{"protected", "open_paths", "eol_product"}
	unsupportedHeartbeat = []string{"kind", "service"}
)
