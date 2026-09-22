package update

import "time"

// Assignment est ce que le serveur pousse à l'agent : les images de la
// machine qu'il ne doit jamais interroger, et « maintenant » quand
// l'opérateur demande une vérification sans attendre la cadence.
type Assignment struct {
	Excluded []string `json:"excluded,omitempty"`
	Now      bool     `json:"now,omitempty"`
}

// CommandImages est le nom de l'événement du flux serveur vers agent.
const CommandImages = "image_checks"

// Outcome dit comment la vérification d'une image s'est finie.
type Outcome string

const (
	// Le registre a répondu : les empreintes et le tag sont là.
	OutcomeOK Outcome = "ok"
	// L'image a été construite sur la machine : aucun registre ne la
	// connaît, il n'y a rien à demander.
	OutcomeLocal Outcome = "local"
	// Le registre n'a pas répondu, ou pas comme un registre.
	OutcomeUnreachable Outcome = "unreachable"
	// Le registre refuse, anonyme comme avec le trousseau : image privée.
	OutcomeUnauthorized Outcome = "unauthorized"
	// Le dépôt ou le tag n'existe pas sur le registre.
	OutcomeNotFound Outcome = "not_found"
	// Une référence qu'on ne sait pas suivre : illisible, ou désignée par
	// son empreinte, donc figée par construction.
	OutcomeUnsupported Outcome = "unsupported"
)

func isOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeOK, OutcomeLocal, OutcomeUnreachable, OutcomeUnauthorized, OutcomeNotFound, OutcomeUnsupported:
		return true
	}
	return false
}

// Result est ce que l'agent a constaté sur une image : des faits, jamais
// une conclusion. Replayed marque ce qui a attendu une reconnexion.
type Result struct {
	Image     string    `json:"image"`
	CheckedAt time.Time `json:"checked_at"`
	Outcome   Outcome   `json:"outcome"`
	// L'empreinte de ce que la machine a tiré, lue dans RepoDigests.
	LocalDigest string `json:"local_digest,omitempty"`
	// L'empreinte de ce que le tag pointe aujourd'hui sur le registre.
	RemoteDigest string `json:"remote_digest,omitempty"`
	// Le tag plus récent écrit de la même façon, et son empreinte.
	NewerTag    string `json:"newer_tag,omitempty"`
	NewerDigest string `json:"newer_digest,omitempty"`
	Replayed    bool   `json:"replayed,omitempty"`
}

// Report est ce que l'agent met dans son signal, à côté des lectures de
// la machine, de ce que son Docker a montré et des essais de ses sondes.
type Report struct {
	Results []Result `json:"results,omitempty"`
}

func (r Report) IsEmpty() bool {
	return len(r.Results) == 0
}

const (
	// Une machine porte au plus 256 conteneurs : autant d'images
	// distinctes, au pire.
	MaxResultsPerReport = 256
	MaxImageLength      = 512
	MaxTagLength        = 128
)
