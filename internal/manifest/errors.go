package manifest

import (
	"errors"
	"sort"
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
)

// KeyExportTooLarge est la clé du catalogue que le serveur rend quand
// l'annuaire ne tient pas dans MaxFileBytes. Ce n'est pas un refus de lecture :
// elle n'est pas dans ProblemKeys.
const KeyExportTooLarge = "manifest.export_too_large"

// ErrExportTooLarge : écrire ce fichier, c'est en produire un que la lecture
// refuserait. Mieux vaut le dire à l'export qu'au retour.
var ErrExportTooLarge = errors.New("the directory is too large for a manifest file")

// Problem est un refus de lecture, à sa ligne. Value est ce que le message
// cite : la clé fautive, la durée mal écrite.
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
}

// ProblemKeys liste les clés qu'une lecture peut rendre, dans l'ordre.
func ProblemKeys() []string {
	keys := make([]string, 0, len(problemArguments))
	for key := range problemArguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Arguments rend ce que le texte du catalogue de la clé met dans ses
// « %d » et ses « %s » : la ligne, la valeur citée, ou l'un des deux.
func (p Problem) Arguments() []any {
	switch problemArguments[p.Key] {
	case lineOnly:
		return []any{p.Line}
	case valueOnly:
		return []any{p.Value}
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
