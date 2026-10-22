package sshkeys

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Les bornes d'un rapport : l'agent les applique en lisant, le serveur les
// vérifie en recevant (un pair peut mentir). 512 clés au plus par machine :
// 64 comptes de 64 clés ne tiendraient pas dans un signal d'un mégaoctet.
const (
	MaxAccounts       = 64
	MaxKeysPerAccount = 64
	MaxKeysPerReport  = 512
	// Un fichier plus gros n'est pas lu ; une ligne plus longue est ignorée
	// (la limite d'une clé publique dans OpenSSH).
	MaxFileBytes    = 256 << 10
	MaxLineBytes    = 16 << 10
	MaxCommentBytes = 64
	MaxAccountName  = 64
	maxIgnored      = MaxFileBytes
	maxBits         = 1 << 17
)

// ErrReportInvalid : le rapport sort de ce qu'un agent peut avoir relevé. Il
// est refusé en entier.
var ErrReportInvalid = errors.New("ssh keys report out of range")

// State dit ce que l'agent a pu lire d'un compte.
type State string

const (
	// StateRead : au moins un fichier lu, et aucun n'a posé problème.
	StateRead State = "read"
	// StateNone : aucun fichier.
	StateNone State = "none"
	// StateDenied : illisible pour le compte de l'agent.
	StateDenied State = "denied"
	// StateUnsafe : un lien, un fichier spécial ou un fichier modifiable par
	// d'autres, que sshd ignorerait.
	StateUnsafe State = "unsafe"
	// StateTooLarge : plus de MaxFileBytes.
	StateTooLarge State = "too_large"
)

func States() []State {
	return []State{StateRead, StateNone, StateDenied, StateUnsafe, StateTooLarge}
}

// Les deux fichiers que sshd lit par défaut (AuthorizedKeysFile).
const (
	FileAuthorizedKeys  = "authorized_keys"
	FileAuthorizedKeys2 = "authorized_keys2"
)

func Files() []string {
	return []string{FileAuthorizedKeys, FileAuthorizedKeys2}
}

// Les types de clé rapportés ; une ligne d'un autre type est ignorée.
func KeyTypes() []string {
	return []string{
		"ssh-ed25519", "ssh-rsa", "ssh-dss",
		"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
		"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com",
	}
}

// OptionOther : une option que la liste ne connaît pas. Son nom vient du
// fichier d'un utilisateur, il n'est jamais rapporté.
const OptionOther = "other"

// Options rend les noms d'option d'authorized_keys que le rapport nomme
// (sshd(8)), en minuscules ; OptionOther pour tout le reste.
func Options() []string {
	return []string{
		"agent-forwarding", "cert-authority", "command", "environment", "expiry-time", "from",
		"no-agent-forwarding", "no-port-forwarding", "no-pty", "no-touch-required", "no-user-rc",
		"no-x11-forwarding", "permitlisten", "permitopen", "port-forwarding", "principals", "pty",
		"restrict", "tunnel", "user-rc", "verify-required", "x11-forwarding", OptionOther,
	}
}

// Key est une clé autorisée : des faits sur la clé publique, rien du reste de
// la ligne. Options ne porte que des noms, jamais une valeur : une commande
// forcée peut contenir un secret.
type Key struct {
	File        string   `json:"file"`
	Fingerprint string   `json:"fingerprint"`
	Type        string   `json:"type"`
	Bits        int      `json:"bits"`
	Comment     string   `json:"comment"`
	Options     []string `json:"options"`
}

// Account est ce que l'agent a relevé d'un compte de la machine.
type Account struct {
	Name  string `json:"name"`
	UID   int64  `json:"uid"`
	State State  `json:"state"`
	// Truncated : le compte porte plus de MaxKeysPerAccount clés (ou le
	// rapport plus de MaxKeysPerReport), les suivantes n'y sont pas.
	Truncated bool `json:"truncated"`
	// Ignored compte les lignes qui ne sont pas une clé d'un type connu.
	Ignored int   `json:"ignored"`
	Keys    []Key `json:"keys"`
}

// Report est le relevé d'une machine, remplacé à chaque fois.
type Report struct {
	Accounts []Account `json:"accounts"`
	// Truncated : la machine a plus de MaxAccounts comptes à lire.
	Truncated bool `json:"truncated"`
}

// Validate refuse en bloc un rapport hors de ce qu'un agent peut avoir relevé.
func (r Report) Validate() error {
	if len(r.Accounts) > MaxAccounts {
		return ErrReportInvalid
	}
	names := map[string]bool{}
	total := 0
	for _, account := range r.Accounts {
		if names[account.Name] {
			return ErrReportInvalid
		}
		names[account.Name] = true
		if err := account.validate(); err != nil {
			return err
		}
		total += len(account.Keys)
	}
	if total > MaxKeysPerReport {
		return ErrReportInvalid
	}
	return nil
}

func (a Account) validate() error {
	if !IsAccountName(a.Name) || a.UID < 0 || a.UID > 4294967295 {
		return ErrReportInvalid
	}
	if !slices.Contains(States(), a.State) || a.Ignored < 0 || a.Ignored > maxIgnored {
		return ErrReportInvalid
	}
	if len(a.Keys) > MaxKeysPerAccount {
		return ErrReportInvalid
	}
	seen := map[string]bool{}
	for _, key := range a.Keys {
		if err := key.validate(); err != nil {
			return err
		}
		identity := key.File + " " + key.Fingerprint
		if seen[identity] {
			return ErrReportInvalid
		}
		seen[identity] = true
	}
	return nil
}

func (k Key) validate() error {
	if !slices.Contains(Files(), k.File) || !slices.Contains(KeyTypes(), k.Type) || !IsFingerprint(k.Fingerprint) {
		return ErrReportInvalid
	}
	if k.Bits < 0 || k.Bits > maxBits || CleanComment(k.Comment) != k.Comment {
		return ErrReportInvalid
	}
	if len(k.Options) > len(Options()) {
		return ErrReportInvalid
	}
	for index, option := range k.Options {
		if !slices.Contains(Options(), option) || slices.Contains(k.Options[:index], option) {
			return ErrReportInvalid
		}
	}
	return nil
}

// IsAccountName : un nom de compte tel que passwd le donne, sans rien qui
// ressemble à un chemin ou à une commande.
func IsAccountName(name string) bool {
	if name == "" || len(name) > MaxAccountName || name[0] == '-' {
		return false
	}
	for _, character := range name {
		isLetter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		isDigit := character >= '0' && character <= '9'
		if !isLetter && !isDigit && character != '_' && character != '.' && character != '@' && character != '-' {
			return false
		}
	}
	return true
}

// IsFingerprint : « SHA256: » et les 43 caractères d'un SHA-256 en base64
// sans bourrage.
func IsFingerprint(text string) bool {
	const prefix = "SHA256:"
	if len(text) != len(prefix)+43 || text[:len(prefix)] != prefix {
		return false
	}
	for _, character := range text[len(prefix):] {
		isLetter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		isDigit := character >= '0' && character <= '9'
		if !isLetter && !isDigit && character != '+' && character != '/' {
			return false
		}
	}
	return true
}

// CleanComment rend le commentaire d'une clé tel qu'il se montre : UTF-8
// valide, sans caractère de contrôle ni de mise en forme (une séquence
// d'échappement, un sens de lecture inversé), bords nettoyés, MaxCommentBytes
// au plus, coupé sur une limite de caractère.
func CleanComment(comment string) string {
	valid := strings.ToValidUTF8(comment, "")
	printable := strings.Map(func(character rune) rune {
		if unicode.IsPrint(character) {
			return character
		}
		return -1
	}, valid)
	return strings.TrimSpace(truncateBytes(strings.TrimSpace(printable), MaxCommentBytes))
}

// truncateBytes coupe un texte valide à limit octets au plus, sans couper un
// caractère en deux.
func truncateBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
