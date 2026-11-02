package agentwire

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// Les ordres de restic. La cible lance rest-server en écriture seule, tient
// ses utilisateurs et applique la rétention chez elle ; la source reçoit
// l'adresse, le certificat de la cible et ses mots de passe, puis sauvegarde.
const (
	KindResticTargetSetup Kind = "restic.target.setup"
	KindResticUserAdd     Kind = "restic.user.add"
	KindResticUserRemove  Kind = "restic.user.remove"
	KindResticProvision   Kind = "restic.provision"
	KindResticBackup      Kind = "restic.backup"
	KindResticRetain      Kind = "restic.retain"
)

// Les bornes du fil restic : ce qu'un pair peut dire au plus.
const (
	maxResticHosts        = 4
	maxResticHostBytes    = 253
	maxResticUsers        = 256
	maxResticLinks        = 64
	maxResticVersionBytes = 64
	maxCertificatePEM     = 8 << 10
	maxServiceNameBytes   = 64
	maxRelayRangeBytes    = 128
	maxRelayAcceptBytes   = 128
	maxRelayQueryBytes    = 32
	// Un compte de rétention, comme la règle du serveur : 0 à 1000.
	maxResticKeep = 1000
	// Au plus ce nombre de volumes, de bases ou de parties rapportées par
	// sauvegarde d'un service.
	maxResticParts = 32
)

// Le chemin d'une sauvegarde : la source joint la cible, ou passe par le
// relais du serveur.
const (
	ResticPathDirect = "direct"
	ResticPathRelay  = "relay"
)

// Pourquoi ce chemin, une liste fermée que l'interface traduit.
const (
	ResticReachable            = "reachable"
	ResticTCPUnreachable       = "tcp_unreachable"
	ResticTLSFailed            = "tls_failed"
	ResticTargetNotProvisioned = "target_not_provisioned"
	ResticRelayForced          = "relay_forced"
)

// ResticPathReasons rend les raisons d'un chemin.
func ResticPathReasons() []string {
	return []string{ResticReachable, ResticTCPUnreachable, ResticTLSFailed, ResticTargetNotProvisioned, ResticRelayForced}
}

// L'état de rest-server sur la cible, et pourquoi il est en erreur.
const (
	ResticTargetRunning = "running"
	ResticTargetStopped = "stopped"
	ResticTargetError   = "error"
)

const (
	ResticErrorBinaryMissing   = "binary_missing"
	ResticErrorStartFailed     = "start_failed"
	ResticErrorPortInUse       = "port_in_use"
	ResticErrorCertificate     = "certificate_unreadable"
	ResticErrorDirUnwritable   = "dir_unwritable"
	ResticErrorHtpasswdInvalid = "htpasswd_invalid"
)

// ResticTargetErrors rend les raisons d'une cible en erreur.
func ResticTargetErrors() []string {
	return []string{ResticErrorBinaryMissing, ResticErrorStartFailed, ResticErrorPortInUse, ResticErrorCertificate, ResticErrorDirUnwritable, ResticErrorHtpasswdInvalid}
}

// Les portées d'une sauvegarde : un invité Proxmox (vzdump en flux) ou le
// dossier d'un service sous la racine de l'agent.
const (
	ResticScopeVM      = "vm"
	ResticScopeService = "service"
)

// ErrResticReportInvalid : la section restic du signal sort de ses bornes. Le
// serveur la refuse en entier (bad_restic), les autres sections passent.
var ErrResticReportInvalid = errors.New("restic report invalid")

// ErrResticRetentionInvalid : une rétention ou un plancher hors bornes.
// L'agent le refuse sous sa raison (retention_invalid), pas en bad_order.
var ErrResticRetentionInvalid = errors.New("restic: retention invalid")

var (
	errResticHost        = errors.New("restic: host invalid")
	errResticPort        = errors.New("restic: port out of range")
	errResticUser        = errors.New("restic: user name invalid")
	errResticPassword    = errors.New("restic: password outside its form")
	errResticCertificate = errors.New("restic: not a single public certificate")
	errResticFingerprint = errors.New("restic: fingerprint does not match the certificate")
	errResticState       = errors.New("restic: state or reason outside the list")
	errResticTooMany     = errors.New("restic: too many entries")
	errResticVersion     = errors.New("restic: version too long or not printable")
	errResticScope       = errors.New("restic: scope invalid")
	errResticTarget      = errors.New("restic: target machine invalid")
	errResticPath        = errors.New("restic: path or reason outside the list")
	errResticSnapshot    = errors.New("restic: snapshot id invalid")
	errResticNegative    = errors.New("restic: negative size or duration")
	errResticRelay       = errors.New("restic: relay request invalid")
)

// Un nom d'utilisateur de dépôt est aussi le premier segment de l'URL
// (--private-repos) et un nom de dossier sur la cible : minuscules, chiffres
// et tirets, ni point ni barre. Un identifiant de machine (UUID) y tient.
var repoUserPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// IsResticUser dit si un nom peut servir d'utilisateur et de dépôt.
func IsResticUser(name string) bool {
	return repoUserPattern.MatchString(name)
}

// ResticUserFor rend l'utilisateur, donc le dépôt, d'une machine source : son
// identifiant en minuscules. Le serveur le pose ; la source ne le choisit pas.
func ResticUserFor(machineID string) (string, error) {
	user := strings.ToLower(machineID)
	if !IsResticUser(user) {
		return "", errResticUser
	}
	return user, nil
}

// bcryptPattern : la forme qu'écrit htpasswd -B ($2y$) et celles que
// rest-server lit aussi ($2a$, $2b$). Rien d'autre n'entre dans le fichier.
var bcryptPattern = regexp.MustCompile(`^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)

// IsBcryptHash dit si une valeur a la forme d'une empreinte bcrypt.
func IsBcryptHash(hash string) bool {
	return bcryptPattern.MatchString(hash)
}

// resticPasswordPattern : la forme d'un mot de passe tiré par le serveur,
// 32 octets de crypto/rand en base64url sans remplissage (43 caractères).
// L'agent admet 32 à 128 caractères de cet alphabet : rien qui demande un
// échappement dans une URL ou un fichier.
var resticPasswordPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)

// IsResticPassword dit si un mot de passe REST ou de dépôt a la forme
// attendue.
func IsResticPassword(password string) bool {
	return resticPasswordPattern.MatchString(password)
}

var hostLabelPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// isResticHost accepte une adresse IP ou un nom DNS, jamais un port ni un
// chemin : il finit dans un certificat et dans une URL.
func isResticHost(host string) bool {
	if host == "" || len(host) > maxResticHostBytes {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for label := range strings.SplitSeq(host, ".") {
		if !hostLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func isPort(port int) bool {
	return port >= 1 && port <= 65535
}

// isPrintable refuse ce qu'un pair glisserait dans un champ affiché : un
// saut de ligne, un caractère de contrôle, un texte trop long.
func isPrintable(text string, limit int) bool {
	if len(text) > limit {
		return false
	}
	for _, char := range text {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

// CertificateFingerprint rend l'empreinte d'un certificat telle que la fiche
// l'affiche : le SHA-256 du DER, en hexadécimal.
func CertificateFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// ParsePublicCertificate lit un PEM qui ne contient qu'un certificat, et
// rien d'autre : une clé privée collée à la suite ne voyage pas.
func ParsePublicCertificate(certificatePEM string) (*x509.Certificate, error) {
	if len(certificatePEM) > maxCertificatePEM {
		return nil, errResticCertificate
	}
	block, rest := pem.Decode([]byte(certificatePEM))
	if block == nil || block.Type != "CERTIFICATE" || strings.TrimSpace(string(rest)) != "" {
		return nil, errResticCertificate
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errResticCertificate
	}
	return certificate, nil
}

// ResticTargetSetupParams : rest-server écoute sur Listen:Port, son
// certificat porte les adresses Hosts que les sources utiliseront. Le
// dossier des dépôts est celui du droit restic.target, pas un paramètre.
// RetentionFloor pose le plancher de rétention de la cible ; absent, la
// cible garde le sien (ou celui par défaut).
type ResticTargetSetupParams struct {
	Listen         string                `json:"listen"`
	Port           int                   `json:"port"`
	Hosts          []string              `json:"hosts"`
	RetentionFloor *ResticRetentionFloor `json:"retention_floor,omitempty"`
}

func (p ResticTargetSetupParams) Validate() error {
	if net.ParseIP(p.Listen) == nil {
		return errResticHost
	}
	if !isPort(p.Port) {
		return errResticPort
	}
	if len(p.Hosts) == 0 || len(p.Hosts) > maxResticHosts {
		return errResticTooMany
	}
	for _, host := range p.Hosts {
		if !isResticHost(host) {
			return errResticHost
		}
	}
	if p.RetentionFloor != nil {
		return p.RetentionFloor.Validate()
	}
	return nil
}

// ResticRetentionFloor est le plancher de rétention que la cible tient
// elle-même : elle refuse un restic.retain qui garderait moins, et un
// plancher plus bas que celui posé. Un serveur compromis ne vide donc pas
// les dépôts par une rétention vide.
type ResticRetentionFloor struct {
	KeepDaily   int `json:"keep_daily"`
	KeepWeekly  int `json:"keep_weekly"`
	KeepMonthly int `json:"keep_monthly"`
}

// DefaultResticRetentionFloor : sept jours, posé à la première préparation
// quand l'ordre n'en dit rien.
func DefaultResticRetentionFloor() ResticRetentionFloor {
	return ResticRetentionFloor{KeepDaily: 7}
}

func (f ResticRetentionFloor) Validate() error {
	for _, count := range []int{f.KeepDaily, f.KeepWeekly, f.KeepMonthly} {
		if count < 0 || count > maxResticKeep {
			return ErrResticRetentionInvalid
		}
	}
	return nil
}

// IsLowerThan dit si ce plancher abaisse l'un des comptes d'un plancher
// déjà posé ; le relever est permis.
func (f ResticRetentionFloor) IsLowerThan(current ResticRetentionFloor) bool {
	return f.KeepDaily < current.KeepDaily || f.KeepWeekly < current.KeepWeekly || f.KeepMonthly < current.KeepMonthly
}

// Admits dit si une rétention garde au moins ce que le plancher exige.
func (f ResticRetentionFloor) Admits(retain ResticRetainParams) bool {
	return retain.KeepDaily >= f.KeepDaily && retain.KeepWeekly >= f.KeepWeekly && retain.KeepMonthly >= f.KeepMonthly
}

// ResticUserAddParams : l'utilisateur, donc le dépôt, d'une source. Son mot
// de passe REST part dans le secret de l'ordre (ResticUserSecret) ; la
// cible en calcule elle-même l'empreinte bcrypt.
type ResticUserAddParams struct {
	User string `json:"user"`
}

func (p ResticUserAddParams) Validate() error {
	if !IsResticUser(p.User) {
		return errResticUser
	}
	return nil
}

// ResticUserSecret est le secret de restic.user.add, signé avec l'ordre :
// le mot de passe REST du lien, que la cible garde pour le relais. Le mot de
// passe du dépôt n'y est jamais lu : la cible ne le garde pas.
type ResticUserSecret struct {
	RESTPassword string `json:"rest_password"` // #nosec G117 -- le champ existe pour porter un secret, le temps d'un ordre.
}

func (s ResticUserSecret) Validate() error {
	if !IsResticPassword(s.RESTPassword) {
		return errResticPassword
	}
	return nil
}

// ResticUserRemoveParams : la ligne disparaît, les données restent.
type ResticUserRemoveParams struct {
	User string `json:"user"`
}

func (p ResticUserRemoveParams) Validate() error {
	if !IsResticUser(p.User) {
		return errResticUser
	}
	return nil
}

// ResticProvisionParams : ce que la source doit savoir d'une cible pour la
// joindre en direct. Rien n'est secret : les mots de passe partent dans le
// secret de l'ordre (ResticProvisionSecret).
type ResticProvisionParams struct {
	Target      string `json:"target"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Certificate string `json:"certificate"`
}

func (p ResticProvisionParams) Validate() error {
	if !IsResticUser(p.Target) {
		return errResticTarget
	}
	if !isResticHost(p.Host) {
		return errResticHost
	}
	if !isPort(p.Port) {
		return errResticPort
	}
	if !IsResticUser(p.User) {
		return errResticUser
	}
	if _, err := ParsePublicCertificate(p.Certificate); err != nil {
		return err
	}
	return nil
}

// ResticProvisionSecret est le secret de restic.provision, signé avec
// l'ordre : les deux mots de passe du lien, tirés et scellés par le serveur.
// La source les garde en 0600 dans son état ; ils ne voyagent plus.
type ResticProvisionSecret struct {
	RESTPassword string `json:"rest_password"` // #nosec G117 -- le champ existe pour porter un secret, le temps d'un ordre.
	RepoPassword string `json:"repo_password"` // #nosec G117 -- idem.
}

func (s ResticProvisionSecret) Validate() error {
	if !IsResticPassword(s.RESTPassword) || !IsResticPassword(s.RepoPassword) {
		return errResticPassword
	}
	return nil
}

// ResticBackupParams : sauvegarder vers la cible liée, un invité Proxmox
// (VMID et sorte) ou un service (son nom de dossier sous la racine).
// ForceRelay saute la sonde : le chemin pris est alors relay_forced.
type ResticBackupParams struct {
	Target     string `json:"target"`
	Scope      string `json:"scope"`
	VMID       int    `json:"vmid,omitempty"`
	GuestType  string `json:"guest_type,omitempty"`
	Service    string `json:"service,omitempty"`
	ForceRelay bool   `json:"force_relay,omitempty"`
	// Portée service : ce que le service a en plus de son dossier. Les
	// volumes nommés (leur nom Docker) et les conteneurs de base de données
	// (leur nom), que le serveur a lus dans l'inventaire de la machine ;
	// l'agent les relit chez lui avant d'y toucher (restic.docker).
	Volumes   []string `json:"volumes,omitempty"`
	Databases []string `json:"databases,omitempty"`
}

// validateService juge ce qu'un service sauvegarde : son dossier (facultatif),
// ses volumes et ses bases. Un service sans rien n'a rien à sauvegarder.
func (p ResticBackupParams) validateService() error {
	if p.Service != "" && (len(p.Service) > maxServiceNameBytes || !serviceNamePattern.MatchString(p.Service)) {
		return errResticScope
	}
	if len(p.Volumes) > maxResticParts || len(p.Databases) > maxResticParts {
		return errResticTooMany
	}
	for _, name := range append(slices.Clone(p.Volumes), p.Databases...) {
		if !IsDockerName(name) {
			return errResticScope
		}
	}
	if p.Service == "" && len(p.Volumes) == 0 && len(p.Databases) == 0 {
		return errResticScope
	}
	return nil
}

// serviceNamePattern : un seul segment de chemin, sans point de tête.
var serviceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func (p ResticBackupParams) Validate() error {
	if !IsResticUser(p.Target) {
		return errResticTarget
	}
	switch p.Scope {
	case ResticScopeVM:
		guest := ProxmoxGuestParams{VMID: p.VMID, Type: p.GuestType}
		if guest.Validate(KindProxmoxSnapshot) != nil || p.Service != "" || len(p.Volumes) > 0 || len(p.Databases) > 0 {
			return errResticScope
		}
	case ResticScopeService:
		if p.VMID != 0 || p.GuestType != "" || p.validateService() != nil {
			return errResticScope
		}
	default:
		return errResticScope
	}
	return nil
}

// ResticRetainParams : la rétention qu'applique la cible sur le dépôt d'une
// source, chez elle (restic forget --prune). Un compte à zéro n'est pas
// passé à restic ; tous à zéro est refusé.
type ResticRetainParams struct {
	User        string `json:"user"`
	KeepLast    int    `json:"keep_last"`
	KeepDaily   int    `json:"keep_daily"`
	KeepWeekly  int    `json:"keep_weekly"`
	KeepMonthly int    `json:"keep_monthly"`
	KeepYearly  int    `json:"keep_yearly"`
}

// Validate rend errResticUser pour un utilisateur hors forme, et
// ErrResticRetentionInvalid pour des comptes hors bornes ou tous nuls.
func (p ResticRetainParams) Validate() error {
	if !IsResticUser(p.User) {
		return errResticUser
	}
	total := 0
	for _, count := range []int{p.KeepLast, p.KeepDaily, p.KeepWeekly, p.KeepMonthly, p.KeepYearly} {
		if count < 0 || count > maxResticKeep {
			return ErrResticRetentionInvalid
		}
		total += count
	}
	if total == 0 {
		return ErrResticRetentionInvalid
	}
	return nil
}

// ResticRetainSecret est le secret de restic.retain : le mot de passe du
// dépôt, que la cible écrit le temps de la commande puis efface.
type ResticRetainSecret struct {
	RepoPassword string `json:"repo_password"` // #nosec G117 -- le champ existe pour porter un secret, le temps d'un ordre.
}

func (s ResticRetainSecret) Validate() error {
	if !IsResticPassword(s.RepoPassword) {
		return errResticPassword
	}
	return nil
}

// Les secrets d'ordre se masquent d'eux-mêmes : un %v ou un journal écrit
// par mégarde ne rend que « [secret] ».
const redactedResticSecret = "[secret]"

func (ResticUserSecret) String() string { return redactedResticSecret }

func (ResticUserSecret) LogValue() slog.Value { return slog.StringValue(redactedResticSecret) }

func (ResticProvisionSecret) String() string { return redactedResticSecret }

func (ResticProvisionSecret) LogValue() slog.Value { return slog.StringValue(redactedResticSecret) }

func (ResticRetainSecret) String() string { return redactedResticSecret }

func (ResticRetainSecret) LogValue() slog.Value { return slog.StringValue(redactedResticSecret) }

// ResticReport est la section restic du signal. Seuls ces champs voyagent :
// le décodeur JSON jette les autres, et Validate borne ceux-ci. Ni mot de
// passe, ni empreinte, ni clé privée.
type ResticReport struct {
	Target *ResticTargetReport `json:"target,omitempty"`
	Source *ResticSourceReport `json:"source,omitempty"`
}

// ResticTargetReport : l'état de rest-server, ce que les sources doivent
// épingler (le certificat public et son empreinte) et le plancher de
// rétention que la cible tient.
type ResticTargetReport struct {
	State          string                `json:"state"`
	Reason         string                `json:"reason,omitempty"`
	Version        string                `json:"version,omitempty"`
	Host           string                `json:"host,omitempty"`
	Port           int                   `json:"port,omitempty"`
	Certificate    string                `json:"certificate,omitempty"`
	Fingerprint    string                `json:"fingerprint,omitempty"`
	RepoBytes      int64                 `json:"repo_bytes"`
	DiskTotal      int64                 `json:"disk_total"`
	DiskFree       int64                 `json:"disk_free"`
	Users          []string              `json:"users,omitempty"`
	RetentionFloor *ResticRetentionFloor `json:"retention_floor,omitempty"`
}

// ResticSourceReport : la version de restic et les cibles liées (celles
// qu'un restic.provision a posées), par leur seul nom.
type ResticSourceReport struct {
	Version string   `json:"version,omitempty"`
	Targets []string `json:"targets,omitempty"`
}

func (r ResticReport) Validate() error {
	if r.Target != nil {
		if err := r.Target.validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrResticReportInvalid, err)
		}
	}
	if r.Source != nil {
		if err := r.Source.validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrResticReportInvalid, err)
		}
	}
	return nil
}

func (r ResticTargetReport) validate() error {
	if err := validateTargetState(r.State, r.Reason); err != nil {
		return err
	}
	if !isPrintable(r.Version, maxResticVersionBytes) {
		return errResticVersion
	}
	if r.Host != "" && !isResticHost(r.Host) {
		return errResticHost
	}
	if r.Port != 0 && !isPort(r.Port) {
		return errResticPort
	}
	if err := validateTargetCertificate(r.Certificate, r.Fingerprint); err != nil {
		return err
	}
	if r.RepoBytes < 0 || r.DiskTotal < 0 || r.DiskFree < 0 {
		return errResticNegative
	}
	if len(r.Users) > maxResticUsers {
		return errResticTooMany
	}
	for _, user := range r.Users {
		if !IsResticUser(user) {
			return errResticUser
		}
	}
	if r.RetentionFloor != nil {
		return r.RetentionFloor.Validate()
	}
	return nil
}

// validateTargetState : une raison n'accompagne que l'état d'erreur.
func validateTargetState(state, reason string) error {
	switch state {
	case ResticTargetRunning, ResticTargetStopped:
		if reason != "" {
			return errResticState
		}
	case ResticTargetError:
		if !slices.Contains(ResticTargetErrors(), reason) {
			return errResticState
		}
	default:
		return errResticState
	}
	return nil
}

// validateTargetCertificate : l'empreinte rapportée est celle du certificat
// rapporté, recalculée ici ; l'un ne vient pas sans l'autre.
func validateTargetCertificate(certificatePEM, fingerprint string) error {
	if certificatePEM == "" && fingerprint == "" {
		return nil
	}
	certificate, err := ParsePublicCertificate(certificatePEM)
	if err != nil {
		return err
	}
	if CertificateFingerprint(certificate.Raw) != fingerprint {
		return errResticFingerprint
	}
	return nil
}

func (r ResticSourceReport) validate() error {
	if !isPrintable(r.Version, maxResticVersionBytes) {
		return errResticVersion
	}
	if len(r.Targets) > maxResticLinks {
		return errResticTooMany
	}
	for _, target := range r.Targets {
		if !IsResticUser(target) {
			return errResticTarget
		}
	}
	return nil
}

// ResticRunReport est le résultat d'une sauvegarde : le chemin pris et
// pourquoi, la cible visée (pour dire quoi ouvrir), et ce que restic a
// écrit. La phrase « pour passer en direct… » est rendue par l'interface.
type ResticRunReport struct {
	Path            string `json:"path"`
	Reason          string `json:"reason"`
	TargetHost      string `json:"target_host,omitempty"`
	TargetPort      int    `json:"target_port,omitempty"`
	SnapshotID      string `json:"snapshot_id,omitempty"`
	BytesAdded      int64  `json:"bytes_added"`
	DurationSeconds int64  `json:"duration_seconds"`
	// Portée service : ce que chaque partie (dossier, volume, base) a donné.
	Parts []ResticPartReport `json:"parts,omitempty"`
}

var snapshotIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (r ResticRunReport) Validate() error {
	if !isPathReason(r.Path, r.Reason) {
		return errResticPath
	}
	if r.TargetHost != "" && !isResticHost(r.TargetHost) {
		return errResticHost
	}
	if r.TargetPort != 0 && !isPort(r.TargetPort) {
		return errResticPort
	}
	if r.SnapshotID != "" && !snapshotIDPattern.MatchString(r.SnapshotID) {
		return errResticSnapshot
	}
	if r.BytesAdded < 0 || r.DurationSeconds < 0 {
		return errResticNegative
	}
	if len(r.Parts) > maxResticParts {
		return errResticTooMany
	}
	for _, part := range r.Parts {
		if err := part.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// isPathReason : le direct n'a qu'une raison, le relais toutes les autres.
func isPathReason(path, reason string) bool {
	switch path {
	case ResticPathDirect:
		return reason == ResticReachable
	case ResticPathRelay:
		return reason != ResticReachable && slices.Contains(ResticPathReasons(), reason)
	}
	return false
}

// ResticRelayRequest est l'événement que le serveur pousse à l'agent de la
// cible pour une requête relayée. Le dépôt est posé par le serveur d'après
// l'identité de la source ; la cible rejuge tout avant d'agir.
type ResticRelayRequest struct {
	ID            string `json:"id"`
	Repo          string `json:"repo"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	Query         string `json:"query,omitempty"`
	Accept        string `json:"accept,omitempty"`
	Range         string `json:"range,omitempty"`
	ContentLength int64  `json:"content_length"`
}

var relayIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// IsRelayID dit si un identifiant a la forme que le relais tire.
func IsRelayID(id string) bool {
	return relayIDPattern.MatchString(id)
}

// Validate borne la forme ; la route elle-même se juge par restic.Allow.
func (r ResticRelayRequest) Validate() error {
	if !IsRelayID(r.ID) || !IsResticUser(r.Repo) || r.ContentLength < -1 {
		return errResticRelay
	}
	if !isPrintable(r.Accept, maxRelayAcceptBytes) || !isPrintable(r.Range, maxRelayRangeBytes) || len(r.Query) > maxRelayQueryBytes {
		return errResticRelay
	}
	return nil
}

// HeaderResticRelayHead porte, sur la réponse que la cible pousse, la tête
// de la réponse de rest-server (ResticRelayHead en JSON) ; le corps suit.
const HeaderResticRelayHead = brand.HeaderPrefix + "Relay-Head"

// ResticRelayHead : le statut et les seuls en-têtes de réponse relayés.
type ResticRelayHead struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ResticRelayResponseHeaders rend les en-têtes de réponse que le relais
// laisse passer ; tous les autres sont jetés.
func ResticRelayResponseHeaders() []string {
	return []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"}
}

// Les routes du relais sous /agent/, toutes authentifiées comme le reste de
// l'agent : la source signe chaque requête de sa preuve (machine.SignedPayload),
// la cible tire le corps et pousse la réponse sous le jeton de session de son
// flux.
const (
	ResticRelayRoute         = "/agent/relay/rest/"
	ResticRelayBodyRoute     = "/agent/relay/body/"
	ResticRelayResponseRoute = "/agent/relay/response/"
)

// ResticRelayPath rend l'adresse du relais vers une cible, sous laquelle le
// mandataire local de la source pose le chemin de chaque requête de restic.
func ResticRelayPath(target string) string {
	return ResticRelayRoute + url.PathEscape(target)
}

// ResticRelayBodyPath rend l'adresse où la cible tire le corps d'une requête.
func ResticRelayBodyPath(id string) string {
	return ResticRelayBodyRoute + url.PathEscape(id)
}

// ResticRelayResponsePath rend l'adresse où la cible pousse la réponse.
func ResticRelayResponsePath(id string) string {
	return ResticRelayResponseRoute + url.PathEscape(id)
}
