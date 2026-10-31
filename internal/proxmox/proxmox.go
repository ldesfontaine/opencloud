package proxmox

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	KindQEMU = "qemu"
	KindLXC  = "lxc"

	// Le contenu d'un stockage qui reçoit les sauvegardes de vzdump.
	ContentBackup = "backup"

	maxURLLength    = 255
	maxTokenIDLen   = 200
	minSecretLength = 8
	maxSecretLength = 200
	pinHexLength    = 64
)

var (
	// ErrNotFound : aucun hôte n'est déclaré sur cette machine.
	ErrNotFound = errors.New("proxmox host not declared")

	ErrURLInvalid     = refusal.Invalid("proxmox.url_invalid", "the API address must be https://host[:port], without path")
	ErrTokenIDInvalid = refusal.Invalid("proxmox.token_id_invalid", "the token identifier must look like user@realm!name")
	ErrSecretInvalid  = refusal.Invalid("proxmox.secret_invalid", "the token secret must be 8 to 200 printable characters, without space")
	ErrPinInvalid     = refusal.Invalid("proxmox.pin_invalid", "the pin must be a SHA-256 fingerprint in hexadecimal")

	// Les quatre façons dont l'API ne répond pas : elles se lisent sur la
	// déclaration refusée comme sur le relevé qui échoue (last_error).
	ErrUnreachable  = refusal.Invalid("proxmox.unreachable", "the Proxmox API did not answer")
	ErrCertificate  = refusal.Invalid("proxmox.certificate", "the certificate of the Proxmox API is not the one expected")
	ErrUnauthorized = refusal.Invalid("proxmox.unauthorized", "the Proxmox API refused the token")
	ErrBadResponse  = refusal.Invalid("proxmox.bad_response", "the Proxmox API answered something unexpected")
)

// Definition est ce que l'opérateur saisit pour déclarer un hôte.
type Definition struct {
	APIURL      string
	TokenID     string
	TokenSecret string
	// Vide : le magasin du système juge le certificat.
	Pin string
}

// Host est l'hôte déclaré, sans son secret.
type Host struct {
	MachineID string
	APIURL    string
	TokenID   string
	Pin       string
	Node      string
	Version   string
	// VerifiedAt : quand le jeton a répondu à la déclaration.
	VerifiedAt time.Time
	// LastReadAt est le dernier relevé réussi ; zéro tant qu'aucun ne l'a été.
	LastReadAt time.Time
	// LastError est la clé du refus du dernier relevé qui a échoué, vide s'il
	// a réussi.
	LastError string
	CreatedAt time.Time
}

// Credentials est l'hôte avec son secret : il ne sort de la base que pour
// parler à l'API.
type Credentials struct {
	Host
	TokenSecret string
}

// Guest est une VM ou un conteneur LXC, tel que l'hyperviseur l'annonce.
type Guest struct {
	VMID     int
	Kind     string
	Name     string
	Status   string
	Template bool
	// Ce qui est alloué, pas ce qui est consommé.
	CPUs    float64
	MaxMem  int64
	MaxDisk int64
	Node    string
	// La machine enrôlée du même nom d'hôte ; vide sans rapprochement.
	LinkedMachineID string
	// Zéro : aucune sauvegarde connue.
	LastBackupAt    time.Time
	LastBackupBytes int64
	// Le stockage de la plus récente, et ce que tous les stockages gardent de
	// cet invité : le nombre d'archives et leur taille totale.
	LastBackupStorage string
	BackupCount       int
	BackupTotalBytes  int64
}

type Storage struct {
	Node  string
	Name  string
	Type  string
	Total int64
	Used  int64
	Avail int64
	// Ce que le stockage accepte : backup, iso, vztmpl…
	Content []string
	Active  bool
}

// AcceptsBackups dit si vzdump peut y écrire ses archives.
func (s Storage) AcceptsBackups() bool {
	for _, content := range s.Content {
		if content == ContentBackup {
			return true
		}
	}
	return false
}

// Reading est un relevé complet de l'API : il remplace le précédent ou rien.
type Reading struct {
	Version  string
	Node     string
	Guests   []Guest
	Storages []Storage
}

// View est ce que la fiche de l'hôte montre.
type View struct {
	Host     Host
	Guests   []Guest
	Storages []Storage
}

// Summary est ce que chaque machine dit de Proxmox : porte-t-elle un hôte,
// combien de VM y a-t-il, et, si elle est elle-même une VM d'un hôte, laquelle.
type Summary struct {
	Declared  bool
	Guests    int
	LastError string
	Hosted    *HostedGuest
}

// HostedGuest est la VM d'un hôte que la machine est.
type HostedGuest struct {
	HostMachineID string
	HostName      string
	Guest
}

var (
	tokenIDPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}@[A-Za-z0-9._-]{1,64}![A-Za-z0-9._-]{1,64}$`)
	// De l'ASCII imprimable sans espace : le secret entre dans un en-tête.
	secretPattern = regexp.MustCompile(`^[\x21-\x7e]+$`)
	pinPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Normalize met la saisie en forme canonique : une adresse sans barre finale,
// une empreinte en minuscules sans deux-points, et rend le premier refus.
func (d Definition) Normalize() (Definition, error) {
	address, err := normalizeURL(d.APIURL)
	if err != nil {
		return Definition{}, err
	}
	d.APIURL = address
	d.TokenID = strings.TrimSpace(d.TokenID)
	if len(d.TokenID) > maxTokenIDLen || !tokenIDPattern.MatchString(d.TokenID) {
		return Definition{}, ErrTokenIDInvalid
	}
	if len(d.TokenSecret) < minSecretLength || len(d.TokenSecret) > maxSecretLength || !secretPattern.MatchString(d.TokenSecret) {
		return Definition{}, ErrSecretInvalid
	}
	d.Pin, err = normalizePin(d.Pin)
	if err != nil {
		return Definition{}, err
	}
	return d, nil
}

// normalizeURL garde le schéma, l'hôte et le port : Proxmox répond en https,
// et ni identifiants, ni chemin, ni requête n'ont de sens ici.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLLength {
		return "", ErrURLInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return "", ErrURLInvalid
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", ErrURLInvalid
	}
	return "https://" + parsed.Host, nil
}

// normalizePin accepte ce que openssl écrit (AA:BB:…), la ligne entière
// comprise (« sha256 Fingerprint=AA:BB:… », que la doc dit de coller telle
// quelle), comme l'hexadécimal nu, avec ou sans « sha256: » devant.
func normalizePin(raw string) (string, error) {
	pin := strings.ToLower(strings.TrimSpace(raw))
	if label, value, found := strings.Cut(pin, "fingerprint="); found && strings.TrimSpace(label) == "sha256" {
		pin = strings.TrimSpace(value)
	}
	pin = strings.TrimPrefix(pin, "sha256:")
	pin = strings.ReplaceAll(pin, ":", "")
	if pin == "" {
		return "", nil
	}
	if len(pin) != pinHexLength || !pinPattern.MatchString(pin) {
		return "", ErrPinInvalid
	}
	return pin, nil
}
