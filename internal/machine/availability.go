package machine

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Ce que l'agent dit de lui à l'ouverture du flux, au-delà de ses droits :
// le compte sous lequel il tourne, et les droits cochés que ce compte ne peut
// pas exercer sur la machine. Les deux se lisent ici, une seule fois, avec
// des bornes : l'agent est un pair qui peut mentir ou être plus vieux.

// Account est le compte système de l'agent. Un nom vide : le compte n'a pas de
// nom lisible, l'uid suffit à dire « root ».
type Account struct {
	UID  int64
	Name string
}

// IsRoot dit si l'agent a tous les droits de la machine.
func (a Account) IsRoot() bool {
	return a.UID == 0
}

const (
	maxAccountUID = 4294967295
	// Un en-tête borné : trois droits jugés par raison, bien en dessous.
	maxUnavailableText = 2048
)

// Un nom de compte POSIX portable, en minuscules : ce que useradd accepte par
// défaut. Un autre (majuscules, point) ne passe pas dans l'en-tête, la fiche
// montre alors l'uid seul.
var accountNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// ErrUnavailableUnreadable : l'en-tête des droits indisponibles ne se lit pas.
// Il est ignoré en entier, comme des droits illisibles.
var ErrUnavailableUnreadable = errors.New("agent unavailable rights unreadable")

// IsAccountName dit si un nom de compte passe dans l'en-tête.
func IsAccountName(name string) bool {
	return accountNamePattern.MatchString(name)
}

// FormatAccount écrit le compte sous la forme de l'en-tête : « uid:nom ».
func FormatAccount(account Account) string {
	return strconv.FormatInt(account.UID, 10) + ":" + account.Name
}

// ParseAccount lit « uid:nom ». L'uid est un entier décimal de 0 à 2^32-1,
// sans signe ni zéro en tête (sauf « 0 ») ; le nom est vide ou de la forme
// d'IsAccountName. Tout le reste est refusé : le texte vient d'un pair.
func ParseAccount(text string) (Account, bool) {
	rawUID, name, found := strings.Cut(text, ":")
	if !found || rawUID == "" || len(rawUID) > 10 || (len(rawUID) > 1 && rawUID[0] == '0') {
		return Account{}, false
	}
	uid, err := strconv.ParseUint(rawUID, 10, 64)
	if err != nil || uid > maxAccountUID {
		return Account{}, false
	}
	if name != "" && !IsAccountName(name) {
		return Account{}, false
	}
	return Account{UID: int64(uid), Name: name}, true // #nosec G115 -- borné juste au-dessus par maxAccountUID.
}

// UnavailableReasons rend la liste fermée des raisons pour lesquelles un droit
// coché ne peut pas s'exercer sur la machine, dans l'ordre où l'agent les
// juge : ce sont des raisons de refus d'agentwire (un test le garde). Elles
// s'écrivent ici parce qu'agentwire importe machine, par probe.
func UnavailableReasons() []string {
	return []string{
		"docker_denied", "docker_missing", "path_denied", "path_unwritable", "not_self_updatable",
		"sudo_missing", "producer_missing", "sudo_denied",
	}
}

// JudgedCapabilities rend les droits dont l'agent juge s'il peut les exercer,
// dans l'ordre de CapabilityNames : ceux qui écrivent sur la machine. Pas
// observe, dont l'état de Docker se lit déjà par le moteur, ni un droit ajouté
// plus tard qui n'écrit rien : la liste est écrite, pas déduite.
func JudgedCapabilities() []string {
	return []string{
		CapabilityAgentUpdate, CapabilityDockerDeploy, CapabilityDockerUpdate, CapabilityBackupReceive,
		CapabilityBackupProduce, CapabilityFilesWrite,
	}
}

// FormatUnavailable écrit les droits indisponibles sous la forme de l'en-tête,
// « droit=raison » séparés par « , », dans l'ordre des droits : deux rapports
// égaux s'écrivent pareil. Vide quand tout est réalisable.
func FormatUnavailable(unavailable map[string]string) string {
	items := make([]string, 0, len(unavailable))
	for _, name := range JudgedCapabilities() {
		if reason := unavailable[name]; reason != "" {
			items = append(items, name+scopeSeparator+reason)
		}
	}
	return strings.Join(items, capabilitySeparator)
}

// ParseUnavailable lit l'en-tête. Un texte vide ne dit rien d'indisponible
// (nil). Un droit qui n'est pas jugé, une raison hors de
// UnavailableReasons, un doublon, un élément sans « = » ou un texte
// trop long refusent l'en-tête entier : on ne garde pas la moitié d'un
// rapport qu'on ne comprend pas.
func ParseUnavailable(text string) (map[string]string, error) {
	if text == "" {
		return nil, nil
	}
	if len(text) > maxUnavailableText {
		return nil, ErrUnavailableUnreadable
	}
	unavailable := map[string]string{}
	for item := range strings.SplitSeq(text, capabilitySeparator) {
		name, reason, found := strings.Cut(strings.TrimSpace(item), scopeSeparator)
		_, duplicate := unavailable[name]
		if !found || duplicate || !slices.Contains(JudgedCapabilities(), name) || !slices.Contains(UnavailableReasons(), reason) {
			return nil, ErrUnavailableUnreadable
		}
		unavailable[name] = reason
	}
	return unavailable, nil
}
