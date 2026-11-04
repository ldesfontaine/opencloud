// Package anchor tient le registre des ancres de confiance locales.
//
// Une ancre est une valeur que seul l'administrateur de la machine pose
// (premier lancement de l'agent, ou « agent trust » en root) : l'agent la
// garde dans son fichier d'identité, la rapporte au serveur, et aucun ordre ne
// peut l'écrire. Un ordre dont un paramètre sort de ses ancres est refusé,
// et sans ancre posée il l'est aussi (refus par défaut).
//
// Chaque type d'ancre se déclare une fois, dans kinds : son nom, ce qu'il
// accepte, combien de valeurs, quels paramètres de quels ordres il borne. Le
// jugement d'un ordre (Judge) ne lit que ce registre.
package anchor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"regexp"
	"slices"
	"strings"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Les types d'ancre, par leur nom. Le nom est aussi celui que l'opérateur
// écrit dans « agent trust ».
const (
	BackupRecipient  = "backup.recipient"
	BackupGuest      = "backup.guest"
	ResticTargetCert = "restic.target-cert"
)

// Bound dit quel paramètre de quel ordre un type d'ancre borne. Les ordres sont
// nommés par leur genre (agentwire.Kind) : agentwire importe ce paquet, pas
// l'inverse ; un test relit la correspondance.
type Bound struct {
	Order string
	// Le champ JSON des paramètres de l'ordre.
	Field string
	// Derive rend la valeur à comparer aux ancres quand ce n'est pas le champ
	// tel quel (l'empreinte d'un certificat). nil : le champ est comparé.
	Derive func(raw string) (string, bool)
}

// Kind est un type d'ancre.
type Kind struct {
	Name string
	// Le droit d'agent dont l'ordre est borné par ce type : la ligne
	// d'installation ne propose le drapeau que si ce droit est coché. Un test
	// relit que c'est un droit connu.
	Right string
	// Le drapeau du premier lancement, sans tiret : plusieurs valeurs
	// séparées par « , ».
	Flag  string
	Max   int
	Valid func(value string) bool
	Bound []Bound
	// Les trois refus d'un ordre, chacun avec sa clé qui nomme l'ancre.
	Missing    *refusal.Error
	Outside    *refusal.Error
	Unreadable *refusal.Error
}

var (
	guestPattern       = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// kinds est le registre. Ajouter un type d'ancre, c'est ajouter une entrée
// ici ; un test (TestKinds_AreTheExpectedOnes) échoue tant que sa liste
// attendue n'est pas mise à jour.
var kinds = []Kind{
	{
		Name: BackupRecipient, Right: "backup.produce", Flag: "backup-recipient", Max: 2, Valid: isAgeRecipient,
		Bound: []Bound{{Order: "backup.produce", Field: "recipient"}},
		Missing: refusal.Conflict("anchor.backup_recipient_missing",
			"no age recipient is trusted on this machine: backup.produce would refuse every order"),
		Outside: refusal.Conflict("anchor.backup_recipient_outside",
			"the age recipient of this order is not one the machine trusts"),
		Unreadable: refusal.Conflict("anchor.backup_recipient_unreadable",
			"the age recipients this machine trusts cannot be read"),
	},
	{
		Name: BackupGuest, Right: "backup.produce", Flag: "backup-guest", Max: 32, Valid: guestPattern.MatchString,
		Bound: []Bound{{Order: "backup.produce", Field: "target"}},
		Missing: refusal.Conflict("anchor.backup_guest_missing",
			"no guest is trusted on this machine: backup.produce would refuse every order"),
		Outside: refusal.Conflict("anchor.backup_guest_outside",
			"the guest of this order is not one the machine trusts"),
		Unreadable: refusal.Conflict("anchor.backup_guest_unreadable",
			"the guests this machine trusts cannot be read"),
	},
	{
		Name: ResticTargetCert, Right: "restic.produce", Flag: "restic-target-cert", Max: 4, Valid: fingerprintPattern.MatchString,
		Bound: []Bound{{Order: "restic.provision", Field: "certificate", Derive: fingerprintOf}},
		Missing: refusal.Conflict("anchor.restic_target_cert_missing",
			"no restic target certificate is trusted on this machine: restic.provision would refuse every order"),
		Outside: refusal.Conflict("anchor.restic_target_cert_outside",
			"the certificate of this restic target is not one the machine trusts"),
		Unreadable: refusal.Conflict("anchor.restic_target_cert_unreadable",
			"the restic target certificates this machine trusts cannot be read"),
	},
}

// Kinds rend le registre, dans l'ordre où l'interface et l'en-tête l'écrivent.
func Kinds() []Kind {
	return slices.Clone(kinds)
}

// Names rend les noms des types d'ancre.
func Names() []string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, kind.Name)
	}
	return names
}

// Lookup rend le type d'ancre de ce nom.
func Lookup(name string) (Kind, bool) {
	for _, kind := range kinds {
		if kind.Name == name {
			return kind, true
		}
	}
	return Kind{}, false
}

// Governed rend les types d'ancre qui bornent cet ordre.
func Governed(order string) []Kind {
	var governing []Kind
	for _, kind := range kinds {
		if slices.ContainsFunc(kind.Bound, func(bound Bound) bool { return bound.Order == order }) {
			governing = append(governing, kind)
		}
	}
	return governing
}

func isAgeRecipient(value string) bool {
	_, err := age.ParseX25519Recipient(value)
	return err == nil
}

// fingerprintOf rend le SHA-256 du DER d'un certificat PEM, en hexadécimal :
// l'empreinte que la fiche d'une cible affiche.
func fingerprintOf(certificatePEM string) (string, bool) {
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", false
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), true
}

// State est ce qu'une machine tient : par type d'ancre, ses valeurs, ou la
// marque qu'elles sont illisibles. Reported est faux chez un agent qui ne dit
// rien de ses ancres (v0.6.0 et avant, qui ne les applique pas) : le serveur
// ne juge alors rien.
type State struct {
	Reported   bool
	Values     map[string][]string
	Unreadable map[string]bool
}

// ValidList dit si une liste de valeurs est une liste d'ancres de ce type : de 1
// à Max valeurs, chacune de la forme du type, sans doublon.
func (k Kind) ValidList(values []string) bool {
	if len(values) == 0 || len(values) > k.Max {
		return false
	}
	for index, value := range values {
		if !k.Valid(value) || slices.Contains(values[:index], value) {
			return false
		}
	}
	return true
}

// FromFile lit les ancres du fichier d'identité. Un type dont la liste n'est
// pas valable est illisible : aucune de ses valeurs ne sert. Un type que le
// registre ne connaît pas est ignoré.
func FromFile(stored map[string][]string) State {
	state := State{Reported: true, Values: map[string][]string{}, Unreadable: map[string]bool{}}
	for _, kind := range kinds {
		values, present := stored[kind.Name]
		switch {
		case !present || len(values) == 0:
		case kind.ValidList(values):
			state.Values[kind.Name] = slices.Clone(values)
		default:
			state.Unreadable[kind.Name] = true
		}
	}
	return state
}

// HasAny dit si au moins un type d'ancre porte une valeur ou est illisible.
func (s State) HasAny() bool {
	return len(s.Values) > 0 || len(s.Unreadable) > 0
}

// Why dit pourquoi un ordre est refusé.
type Why string

const (
	WhyMissing    Why = "missing"
	WhyUnreadable Why = "unreadable"
	WhyOutside    Why = "outside"
)

// Finding est un refus par ancre : quel type, pourquoi, et la valeur de l'ordre
// que l'opérateur devrait poser pour le laisser passer (vide si elle n'est pas
// lisible).
type Finding struct {
	Kind  Kind
	Why   Why
	Value string
}

// Refusal rend le refus déclaré par le type pour cette cause.
func (f Finding) Refusal() *refusal.Error {
	switch f.Why {
	case WhyMissing:
		return f.Kind.Missing
	case WhyUnreadable:
		return f.Kind.Unreadable
	}
	return f.Kind.Outside
}

// Command rend la commande que l'opérateur lance en root sur la machine pour
// poser l'ancre qui manque. Vide quand la valeur de l'ordre n'est pas lisible.
// Fix rend toujours une commande.
func (f Finding) Command() string {
	if f.Value == "" {
		return ""
	}
	return TrustCommand("add", f.Kind.Name, f.Value)
}

// CommandFor rend la commande qui poserait l'ancre du type que cet ordre a fait
// refuser, avec la valeur de ses paramètres ; vide si le type est inconnu ou si
// la valeur n'a pas la forme du type. Le serveur la montre à côté d'un run
// refusé par une ancre.
func CommandFor(kindName, order string, params json.RawMessage) string {
	kind, known := Lookup(kindName)
	if !known {
		return ""
	}
	value, ok := valueOf(kind, order, params)
	if !ok {
		return ""
	}
	return TrustCommand("add", kind.Name, value)
}

// FixFor rend la commande à lancer en root pour lever le refus d'un type
// d'ancre : exacte (« add <type> <valeur> ») quand les paramètres de l'ordre
// donnent la valeur, sinon « trust list », qui montre ce que l'agent tient, et
// exact est faux : l'écran ajoute alors un lien vers la fiche de la machine.
func FixFor(kindName, order string, params json.RawMessage) (command string, exact bool) {
	if command := CommandFor(kindName, order, params); command != "" {
		return command, true
	}
	return TrustCommand("list", "", ""), false
}

// TrustCommand écrit la commande locale, telle qu'on la colle. Le binaire est
// celui que l'installation recommandée pose.
func TrustCommand(verb, kind, value string) string {
	return strings.TrimSpace(brand.AgentCurrentPath + " agent trust " + verb + " " + kind + " " + value)
}

// Judge juge un ordre contre les ancres : le premier type qui le borne et qu'il
// ne satisfait pas le refuse. nil : l'ordre passe, ou aucun type ne le borne.
// Des paramètres illisibles ne passent pas : ils sortent de toute ancre.
func Judge(state State, order string, params json.RawMessage) *Finding {
	for _, kind := range Governed(order) {
		if finding := judgeKind(state, kind, order, params); finding != nil {
			return finding
		}
	}
	return nil
}

func judgeKind(state State, kind Kind, order string, params json.RawMessage) *Finding {
	value, ok := valueOf(kind, order, params)
	// Une valeur qui n'a pas la forme du type ne se pose pas : pas de commande.
	if !ok {
		value = ""
	}
	switch {
	case state.Unreadable[kind.Name]:
		return &Finding{Kind: kind, Why: WhyUnreadable, Value: value}
	case len(state.Values[kind.Name]) == 0:
		return &Finding{Kind: kind, Why: WhyMissing, Value: value}
	case !ok || !slices.Contains(state.Values[kind.Name], value):
		return &Finding{Kind: kind, Why: WhyOutside, Value: value}
	}
	return nil
}

// valueOf lit dans les paramètres la valeur que le type compare à ses ancres.
func valueOf(kind Kind, order string, params json.RawMessage) (string, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return "", false
	}
	for _, bound := range kind.Bound {
		if bound.Order != order {
			continue
		}
		var raw string
		if err := json.Unmarshal(fields[bound.Field], &raw); err != nil {
			return "", false
		}
		if bound.Derive == nil {
			return raw, kind.Valid(raw)
		}
		return bound.Derive(raw)
	}
	return "", false
}

// JudgeReported juge pour le serveur : il ne refuse que ce que l'agent a dit
// refuser. Un agent qui n'a rien rapporté de ses ancres les ignore (v0.6.0).
func JudgeReported(state State, order string, params json.RawMessage) *Finding {
	if !state.Reported {
		return nil
	}
	return Judge(state, order, params)
}

// L'en-tête et le drapeau écrivent une liste de valeurs avec « : », les types
// avec « , » : aucun des trois alphabets (age1…, chiffres, hexadécimal) ne les
// porte.
const (
	listSeparator = ","
	valueSplit    = ":"
	typeSplit     = "="
	// Un type illisible s'écrit avec ce signe à la place de ses valeurs.
	unreadableMark = "!"
	// Rien de posé : l'en-tête n'est jamais vide, il dit que l'agent sait ses
	// ancres.
	noneText = "none"
	// Bien au-delà de 2 + 32 + 4 valeurs de leur longueur.
	maxHeaderBytes = 4096
)

// ErrUnreadable : un texte d'ancres qui ne se lit pas ; il est ignoré en entier.
var ErrUnreadable = errors.New("anchors unreadable")

// Format écrit les ancres sous la forme de l'en-tête, dans l'ordre du registre
// : deux états égaux s'écrivent pareil. Un type sans valeur n'y figure pas.
func (s State) Format() string {
	var items []string
	for _, kind := range kinds {
		switch {
		case s.Unreadable[kind.Name]:
			items = append(items, kind.Name+typeSplit+unreadableMark)
		case len(s.Values[kind.Name]) > 0:
			items = append(items, kind.Name+typeSplit+strings.Join(s.Values[kind.Name], valueSplit))
		}
	}
	if len(items) == 0 {
		return noneText
	}
	return strings.Join(items, listSeparator)
}

// Parse lit l'en-tête. Vide : l'agent n'a rien dit (Reported faux). Un type
// inconnu, une valeur hors de sa forme, un doublon ou un texte trop long
// refusent l'en-tête entier : le texte vient d'un pair.
func Parse(text string) (State, error) {
	if text == "" {
		return State{}, nil
	}
	state := State{Reported: true, Values: map[string][]string{}, Unreadable: map[string]bool{}}
	if text == noneText {
		return state, nil
	}
	if len(text) > maxHeaderBytes {
		return State{}, ErrUnreadable
	}
	for item := range strings.SplitSeq(text, listSeparator) {
		name, list, found := strings.Cut(item, typeSplit)
		kind, known := Lookup(name)
		_, seenValues := state.Values[name]
		if !found || !known || seenValues || state.Unreadable[name] {
			return State{}, ErrUnreadable
		}
		if list == unreadableMark {
			state.Unreadable[name] = true
			continue
		}
		values := strings.Split(list, valueSplit)
		if !kind.ValidList(values) {
			return State{}, ErrUnreadable
		}
		state.Values[name] = values
	}
	return state, nil
}

// ParseList lit la valeur d'un drapeau ou d'une ligne d'installation : des
// valeurs séparées par « , », toutes de la forme du type. Vide : aucune.
func ParseList(kind Kind, text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var values []string
	for value := range strings.SplitSeq(text, listSeparator) {
		values = append(values, strings.TrimSpace(value))
	}
	if !kind.ValidList(values) {
		return nil, ErrUnreadable
	}
	return values, nil
}

// FormatList écrit des valeurs pour un drapeau.
func FormatList(values []string) string {
	return strings.Join(values, listSeparator)
}

// Add ajoute une valeur au type, sans doublon, dans les bornes du type.
func Add(stored map[string][]string, kind Kind, value string) (map[string][]string, error) {
	if !kind.Valid(value) {
		return nil, ErrUnreadable
	}
	updated := Clone(stored)
	if !slices.Contains(updated[kind.Name], value) {
		updated[kind.Name] = append(updated[kind.Name], value)
	}
	if len(updated[kind.Name]) > kind.Max {
		return nil, ErrTooMany
	}
	if !kind.ValidList(updated[kind.Name]) {
		return nil, ErrStoredUnreadable
	}
	return updated, nil
}

// ErrTooMany : le type porte déjà son maximum de valeurs.
var ErrTooMany = errors.New("anchor kind is full")

// ErrStoredUnreadable : la liste du fichier est illisible ; on en retire la
// valeur fautive avant d'en ajouter une.
var ErrStoredUnreadable = errors.New("stored anchors are unreadable")

// ErrAbsent : la valeur à retirer n'est pas posée.
var ErrAbsent = errors.New("anchor value is not set")

// Remove retire une valeur du type ; un type sans valeur disparaît du fichier.
func Remove(stored map[string][]string, kind Kind, value string) (map[string][]string, error) {
	updated := Clone(stored)
	index := slices.Index(updated[kind.Name], value)
	if index < 0 {
		return nil, ErrAbsent
	}
	updated[kind.Name] = slices.Delete(updated[kind.Name], index, index+1)
	if len(updated[kind.Name]) == 0 {
		delete(updated, kind.Name)
	}
	return updated, nil
}

// Clone copie le contenu du fichier d'identité avant de le changer.
func Clone(stored map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(stored))
	for name, values := range stored {
		cloned[name] = slices.Clone(values)
	}
	return cloned
}

// Refused est le refus d'un ordre par une ancre : une erreur de refus, dont la
// clé nomme l'ancre, qui porte la commande que l'opérateur lance sur la
// machine pour le lever. Le serveur la rend sans envoyer l'ordre.
type Refused struct {
	Finding Finding
}

func (r *Refused) Error() string {
	return r.Finding.Refusal().Error()
}

// Fix rend la commande du refus et dit si elle pose la valeur de l'ordre.
func (r *Refused) Fix() (command string, exact bool) {
	if command := r.Finding.Command(); command != "" {
		return command, true
	}
	return TrustCommand("list", "", ""), false
}

// Unwrap laisse refusal.KeyOf lire la clé du type d'ancre.
func (r *Refused) Unwrap() error {
	return r.Finding.Refusal()
}

// Command rend la commande qui poserait l'ancre ; vide si l'ordre n'en dit pas
// la valeur.
func (r *Refused) Command() string {
	return r.Finding.Command()
}
