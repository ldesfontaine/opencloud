package agentwire

import (
	"errors"
	"log/slog"
)

// KindResticVerify : la cible vérifie le dépôt d'une source chez elle, sans
// que personne saisisse rien (le mot de passe du dépôt vient du serveur, dans
// le secret signé de l'ordre). Deux temps : restic check, puis la relecture
// d'un échantillon de fichiers du dernier instantané de chaque ensemble de
// chemins. Rien n'est écrit sur le disque.
const KindResticVerify Kind = "restic.verify"

// Les bornes de ce que la cible rapporte d'une vérification.
const (
	maxVerifySnapshots = 64
	maxVerifyFiles     = 512
	maxVerifyBytes     = int64(1) << 40
)

var errResticVerify = errors.New("restic verify report: counts out of bounds")

// ResticVerifyParams : le dépôt visé, par son utilisateur.
type ResticVerifyParams struct {
	User string `json:"user"`
}

func (p ResticVerifyParams) Validate() error {
	if !IsResticUser(p.User) {
		return errResticUser
	}
	return nil
}

// ResticVerifySecret : le mot de passe du dépôt, que la cible écrit le temps
// de la commande puis efface.
type ResticVerifySecret struct {
	RepoPassword string `json:"repo_password"` // #nosec G117 -- le champ existe pour porter un secret, le temps d'un ordre.
}

func (s ResticVerifySecret) Validate() error {
	if !IsResticPassword(s.RepoPassword) {
		return errResticPassword
	}
	return nil
}

func (ResticVerifySecret) String() string { return redactedResticSecret }

func (ResticVerifySecret) LogValue() slog.Value { return slog.StringValue(redactedResticSecret) }

// ResticVerifyReport est tout ce que le serveur apprend d'une vérification
// réussie : des comptes. Ni nom de fichier, ni chemin, ni contenu, ni sortie
// de restic : ce qui est relu appartient à la source.
type ResticVerifyReport struct {
	// Les instantanés relus (le plus récent de chaque ensemble de chemins).
	Snapshots int `json:"snapshots"`
	// Les fichiers relus, et leurs octets.
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

func (r ResticVerifyReport) Validate() error {
	if r.Snapshots < 0 || r.Snapshots > maxVerifySnapshots || r.Files < 0 || r.Files > maxVerifyFiles ||
		r.Bytes < 0 || r.Bytes > maxVerifyBytes {
		return errResticVerify
	}
	return nil
}
