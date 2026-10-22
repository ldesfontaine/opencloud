package agentwire

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// OrderBody est ce que la clé du serveur signe : quel ordre, pour quelle
// machine et quel agent, jusqu'à quand, à quel rang. L'agent refuse un ordre
// qui n'est pas pour lui, périmé, ou dont le rang ne dépasse pas le dernier
// qu'il a exécuté.
type OrderBody struct {
	RunID     string          `json:"run_id"`
	MachineID string          `json:"machine_id"`
	AgentID   string          `json:"agent_id"`
	Kind      Kind            `json:"kind"`
	Params    json.RawMessage `json:"params,omitempty"`
	// Signé avec l'ordre ; jamais écrit dans un run, un journal ou l'audit :
	// il ne vit que le temps de l'exécution (le .env d'un déploiement).
	Secret json.RawMessage `json:"secret,omitempty"` // #nosec G117 -- le champ existe pour porter un secret, le temps d'un ordre.
	// Secondes Unix : au-delà, l'agent refuse l'ordre.
	NotAfter int64 `json:"not_after"`
	// Croissant par agent : l'anti-rejeu de l'agent.
	Seq int64 `json:"seq"`
}

// MaxSecretBytes borne le champ secret d'un ordre : l'enveloppe entière doit
// tenir dans un événement (MaxEventBytes).
const MaxSecretBytes = 64 << 10

// SignedOrder est l'enveloppe qui voyage : le corps tel qu'il a été signé,
// octet pour octet, sa signature Ed25519 et l'empreinte de la clé qui l'a
// faite. L'agent vérifie ces octets-là avant d'en lire un seul champ :
// aucune forme canonique à recalculer.
type SignedOrder struct {
	Body      []byte `json:"body"`
	Signature []byte `json:"signature"`
	Key       string `json:"key"`
}

// OrderSigningBytes rend ce que la clé du serveur signe pour un corps :
// l'étiquette de domaine puis le corps.
func OrderSigningBytes(body []byte) []byte {
	signed := make([]byte, 0, len(brand.OrderSignatureDomain)+len(body))
	signed = append(signed, brand.OrderSignatureDomain...)
	return append(signed, body...)
}

// DecodeSignedOrder lit l'enveloppe reçue par le flux, sans rien vérifier.
func DecodeSignedOrder(data []byte) (SignedOrder, error) {
	var order SignedOrder
	if err := json.Unmarshal(data, &order); err != nil {
		return SignedOrder{}, fmt.Errorf("decode order: %w", err)
	}
	return order, nil
}

// KeyFingerprint est l'empreinte d'une clé publique telle que l'opérateur
// la compare : « SHA256: » suivi du condensé en base64, comme OpenSSH.
func KeyFingerprint(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// L'état qu'un agent rapporte d'un ordre, dans la section actions de son
// signal. Le serveur tient les autres (queued, sent, expired).
type ActionState string

const (
	ActionAccepted  ActionState = "accepted"
	ActionRefused   ActionState = "refused"
	ActionSucceeded ActionState = "succeeded"
	ActionFailed    ActionState = "failed"
)

// Les raisons d'un refus, une liste fermée que l'interface traduit.
const (
	ReasonServerKeyMissing = "server_key_missing"
	ReasonBadSignature     = "bad_signature"
	ReasonBadOrder         = "bad_order"
	ReasonWrongMachine     = "wrong_machine"
	ReasonWrongAgent       = "wrong_agent"
	ReasonExpired          = "expired"
	ReasonReplayed         = "replayed"
	ReasonUnknownKind      = "unknown_kind"
	// Suivi de « : » et du droit qui manque : right_missing:agent.update.
	ReasonRightMissing    = "right_missing"
	ReasonStateUnwritable = "state_unwritable"
	ReasonNotImplemented  = "not_implemented"
	// Les raisons des ordres qui écrivent : le chemin n'est pas couvert par le
	// droit de l'agent, ou son unité ne l'autorise pas ; le colis n'a pas pu
	// être téléchargé, ne correspond pas à la somme signée, ou n'est pas ce
	// qu'il prétend être.
	ReasonPathOutsideRight = "path_outside_right"
	ReasonPathUnwritable   = "path_unwritable"
	ReasonDownloadFailed   = "download_failed"
	ReasonDigestMismatch   = "digest_mismatch"
	ReasonNotAgeFile       = "not_an_age_file"
	// La file des ordres longs de l'agent est pleine.
	ReasonBusy = "busy"
)

// RightMissing rend la raison d'un ordre refusé faute du droit nommé.
func RightMissing(right string) string {
	return ReasonRightMissing + ":" + right
}

// RefusalReasons rend la liste, dans l'ordre où l'agent juge.
func RefusalReasons() []string {
	return []string{
		ReasonServerKeyMissing, ReasonBadSignature, ReasonBadOrder, ReasonWrongMachine,
		ReasonWrongAgent, ReasonExpired, ReasonReplayed, ReasonUnknownKind, ReasonRightMissing,
		ReasonStateUnwritable, ReasonNotImplemented, ReasonPathOutsideRight, ReasonPathUnwritable,
		ReasonDownloadFailed, ReasonDigestMismatch, ReasonNotAgeFile, ReasonBusy,
	}
}

// Les bornes d'un rapport : ce qu'un signal porte au plus, et ce que le
// serveur garde d'une sortie.
const (
	MaxActionResults   = 32
	MaxOutputLines     = 64
	MaxOutputLineBytes = 4 << 10
)

// ActionReport est la section actions du signal : ce que l'agent a fait des
// ordres reçus depuis le signal précédent.
type ActionReport struct {
	Results []ActionResult `json:"results,omitempty"`
}

func (r ActionReport) IsEmpty() bool {
	return len(r.Results) == 0
}

type ActionResult struct {
	RunID  string       `json:"run_id"`
	State  ActionState  `json:"state"`
	Reason string       `json:"reason,omitempty"`
	Output []OutputLine `json:"output,omitempty"`
}

// OutputLine est une ligne de la sortie d'un ordre ; stream vaut « stdout »
// ou « stderr ».
type OutputLine struct {
	Stream string `json:"stream"`
	Line   string `json:"line"`
}
