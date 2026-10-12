package lifecycle

import "time"

// Cycle est une branche d'un produit telle que la source la décrit. Les
// dates restent des jours, « 2028-11-09 », vides quand la source n'en
// annonce pas.
type Cycle struct {
	Name         string `json:"name"`
	IsEOL        bool   `json:"is_eol"`
	EOLFrom      string `json:"eol_from"`
	IsMaintained bool   `json:"is_maintained"`
	// La dernière version publiée de la branche ; vide si la source l'ignore.
	Latest string `json:"latest"`
}

type Product struct {
	Name   string  `json:"name"`
	Label  string  `json:"label"`
	Cycles []Cycle `json:"cycles"`
	// Quand ce produit a répondu pour la dernière fois : un produit muet
	// garde ses branches, et leur âge se voit.
	FetchedAt time.Time `json:"fetched_at"`
}

// Snapshot est tout ce qu'openCloud garde de la source, remplacé d'un bloc
// à chaque lecture réussie : quel dépôt d'images est quel produit, et les
// branches des produits que les services utilisent.
type Snapshot struct {
	FetchedAt time.Time `json:"fetched_at"`
	// Dépôt Docker (« library/postgres ») → nom du produit (« postgresql »).
	Repositories map[string]string  `json:"repositories"`
	Products     map[string]Product `json:"products"`
	// Les produits demandés que la source dit ne pas avoir.
	NotFound map[string]bool `json:"not_found,omitempty"`
}

// Status est ce qu'on sait dire du support d'une image.
type Status string

const (
	// La branche reçoit encore des correctifs.
	StatusMaintained Status = "maintained"
	// La branche n'en reçoit plus.
	StatusEOL Status = "eol"
	// La source ne relie cette image à aucun produit.
	StatusUnknownProduct Status = "unknown_product"
	// Le produit est connu, mais le tag ne dit pas quelle branche tourne.
	StatusUnknownVersion Status = "unknown_version"
	// Activé, mais la source n'a pas encore été lue pour ce produit.
	StatusPending Status = "pending"
)

func Statuses() []Status {
	return []Status{StatusMaintained, StatusEOL, StatusUnknownProduct, StatusUnknownVersion, StatusPending}
}

// Newer est une branche maintenue plus récente que celle qui tourne.
type Newer struct {
	Cycle   string
	Latest  string
	EOLFrom string
}

// Verdict est ce que la fiche d'un service affiche : le produit reconnu,
// la branche qui tourne, son support, et vers quoi monter.
type Verdict struct {
	Status  Status
	Product string
	// La page du produit chez la source, pour vérifier ce qui a été reconnu.
	ProductURL string
	Cycle      string
	EOLFrom    string
	Latest     string
	Newer      []Newer
	FetchedAt  time.Time
	// La dernière lecture a plus de StaleAfter : ce qu'on affiche date.
	IsStale bool
}

// State est ce que Paramètres affiche de la dernière lecture.
type State struct {
	FetchedAt time.Time
	Products  int
	// Le dernier essai a échoué ; LastAttemptAt dit quand.
	IsFailing     bool
	LastAttemptAt time.Time
}
