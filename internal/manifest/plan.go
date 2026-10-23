package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/deletion"
)

// Action est ce qu'un changement fait à son objet.
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

func Actions() []Action {
	return []Action{ActionCreate, ActionUpdate, ActionDelete}
}

// Kind est le genre d'objet qu'un changement touche. Jamais la machine : le
// fichier la lit, il ne la crée ni ne la supprime.
type Kind string

const (
	KindProject   Kind = "project"
	KindService   Kind = "service"
	KindProbe     Kind = "probe"
	KindHeartbeat Kind = "heartbeat"
	KindIgnored   Kind = "ignored"
)

func Kinds() []Kind {
	return []Kind{KindProject, KindService, KindProbe, KindHeartbeat, KindIgnored}
}

// Note est une conséquence que le plan dit avant qu'on confirme. Chacune a
// son texte dans le catalogue, sous manifest.note_<note>.
type Note string

const (
	NoteProbeResets            Note = "probe_resets"
	NoteHeartbeatDeadlineMoves Note = "heartbeat_deadline_moves"
	NoteUnitRemoved            Note = "unit_removed"
	NoteUnitAdded              Note = "unit_added"
	NoteServiceMoves           Note = "service_moves"
	NoteTokenGenerated         Note = "token_generated"
	// NotePasswordChanged : le mot de passe de la cible change ; le plan ne le
	// montre pas, il le dit.
	NotePasswordChanged Note = "password_changed"
)

func Notes() []Note {
	return []Note{
		NoteProbeResets, NoteHeartbeatDeadlineMoves, NoteUnitRemoved, NoteUnitAdded,
		NoteServiceMoves, NoteTokenGenerated, NotePasswordChanged,
	}
}

// Ce qu'une suppression emporte avec elle ; chaque genre a son texte sous
// manifest.carries_<genre>. L'historique est compté par le plan de
// suppression du domaine (deletion.Plan) : mêmes noms de genre.
const (
	CarryProbeResults   = string(deletion.KindProbeResults)
	CarryProbeDays      = string(deletion.KindProbeDays)
	CarryHeartbeatPings = string(deletion.KindHeartbeatPings)
	CarryHeartbeatRuns  = string(deletion.KindHeartbeatRuns)
	CarrySilences       = string(deletion.KindSilences)
	CarryUnits          = string(deletion.KindUnits)
	CarryProjectLinks   = string(deletion.KindProjectLinks)
)

func CarryKinds() []string {
	return []string{
		CarryProbeResults, CarryProbeDays, CarryHeartbeatPings, CarryHeartbeatRuns, CarrySilences,
		CarryUnits, CarryProjectLinks,
	}
}

// FieldChange dit un champ qui change, dans le vocabulaire du fichier : les
// noms de champ et les valeurs s'y lisent tels qu'on les y écrit.
type FieldChange struct {
	Field  string
	Before string
	After  string
}

// Carry est ce qu'une suppression emporte : un genre et un nombre.
type Carry struct {
	What  string
	Count int
}

// Change est un geste du plan : un objet créé, modifié ou supprimé. L'ID est
// celui de l'objet existant, vide pour une création (il n'existe pas encore).
type Change struct {
	Action  Action
	Kind    Kind
	ID      string
	Name    string
	Fields  []FieldChange
	Notes   []Note
	Carries []Carry
}

// Label est le nom du changement dans un journal ou un message d'erreur :
// genre et nom, jamais un secret.
func (c Change) Label() string {
	return string(c.Kind) + "/" + c.Name
}

// Ref désigne un objet de la base qu'aucune entrée du fichier ne nomme.
type Ref struct {
	Kind Kind
	ID   string
	Name string
}

// Plan est ce que le fichier changerait. Il ne vaut que sans refus : voir
// Applicable. Les changements sont dans l'ordre d'écriture, qui est aussi
// l'ordre affiché.
type Plan struct {
	FileHash string
	Prune    bool
	Changes  []Change
	// Unchanged compte les entrées déjà conformes ; elles ne s'écrivent pas.
	Unchanged int
	// Unmanaged liste ce que la base garde sans que le fichier le nomme, quand
	// --prune n'est pas demandé.
	Unmanaged []Ref
	Problems  []Problem
	// Hash identifie ce que l'écriture ferait : l'application le compare à
	// celui que l'opérateur a confirmé.
	Hash string

	// Ce que l'application exécute ; jamais publié. projects et services
	// disent l'identifiant de chaque projet et service de l'état visé, par
	// nom : réel, ou en attente tant que le plan ne l'a pas créé.
	steps    []step
	projects map[string]string
	services map[string]string
}

// Applicable dit que le plan n'a aucun refus : un seul suffit à tout
// interdire, rien ne s'écrit à moitié sur la foi d'un fichier douteux.
func (p Plan) Applicable() bool {
	return len(p.Problems) == 0
}

// Counts compte les changements par action.
func (p Plan) Counts() (created, updated, deleted int) {
	for _, change := range p.Changes {
		switch change.Action {
		case ActionCreate:
			created++
		case ActionUpdate:
			updated++
		case ActionDelete:
			deleted++
		}
	}
	return created, updated, deleted
}

// hashedChange est ce que l'empreinte lit d'un changement. Les comptes
// d'historique (essais, jours, pings, exécutions) n'y sont pas : l'historique
// grossit pendant qu'on relit le plan, et ce n'est pas ce que le fichier fait.
// Les comptes de structure (unités, liens de projet) y sont : une unité
// ajoutée dans l'interface à un service que la suppression emporte change ce
// que l'opérateur a confirmé.
type hashedChange struct {
	Action  Action        `json:"action"`
	Kind    Kind          `json:"kind"`
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Fields  []FieldChange `json:"fields"`
	Notes   []Note        `json:"notes"`
	Carries []Carry       `json:"carries"`
}

// structuralCarry dit si un compte décrit la structure de l'annuaire plutôt
// que son historique.
func structuralCarry(what string) bool {
	return what == CarryUnits || what == CarryProjectLinks
}

func structuralCarries(carries []Carry) []Carry {
	kept := []Carry{}
	for _, carry := range carries {
		if structuralCarry(carry.What) {
			kept = append(kept, carry)
		}
	}
	return kept
}

type hashedPlan struct {
	Prune   bool           `json:"prune"`
	Changes []hashedChange `json:"changes"`
}

// hashChanges est l'empreinte du plan : sha256 du JSON de (prune, changements
// avec leurs valeurs d'avant). Une édition faite dans l'interface entre le
// plan et l'application change donc l'empreinte.
func hashChanges(prune bool, changes []Change) string {
	hashed := hashedPlan{Prune: prune, Changes: make([]hashedChange, 0, len(changes))}
	for _, change := range changes {
		hashed.Changes = append(hashed.Changes, hashedChange{
			Action: change.Action, Kind: change.Kind, ID: change.ID, Name: change.Name,
			Fields: nonNilFields(change.Fields), Notes: nonNilNotes(change.Notes),
			Carries: structuralCarries(change.Carries),
		})
	}
	encoded, err := json.Marshal(hashed)
	if err != nil {
		// Des chaînes et des booléens : l'encodage ne peut pas échouer.
		panic(fmt.Sprintf("hash plan: %v", err))
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func nonNilFields(fields []FieldChange) []FieldChange {
	if fields == nil {
		return []FieldChange{}
	}
	return fields
}

func nonNilNotes(notes []Note) []Note {
	if notes == nil {
		return []Note{}
	}
	return notes
}

// HashBytes est l'empreinte d'un fichier tel qu'on l'a reçu.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// joinLabels écrit des noms séparés par une virgule, pour un champ ou une
// phrase.
func joinLabels(labels []string) string {
	return strings.Join(labels, ", ")
}
