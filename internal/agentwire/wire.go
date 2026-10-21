package agentwire

import (
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Ce que l'agent et le serveur échangent en JSON pour l'enrôlement et le
// défi ; les clés et octets voyagent en base64 standard. Les en-têtes
// portent la preuve signée qui ouvre le flux.
const (
	HeaderMachine      = brand.HeaderPrefix + "Machine"
	HeaderNonce        = brand.HeaderPrefix + "Nonce"
	HeaderTimestamp    = brand.HeaderPrefix + "Timestamp"
	HeaderSignature    = brand.HeaderPrefix + "Signature"
	HeaderAgentVersion = brand.HeaderPrefix + "Agent-Version"
	// Le système de la machine, redit à chaque ouverture du flux :
	// l'enrôlement le fige, une montée de version le change. Absent chez un
	// agent plus vieux.
	HeaderAgentOS = brand.HeaderPrefix + "Agent-OS"
	// Posé par un agent qui sait lire un refus par section ; sans lui, le
	// serveur répond comme avant, par un 400 global.
	HeaderSignalSections = brand.HeaderPrefix + "Signal-Sections"
	// Les droits que l'agent lit dans son fichier, redits à chaque ouverture
	// du flux, sous leur forme texte (machine.FormatCapabilities). Absent
	// chez un agent plus vieux : rien n'est rapporté.
	HeaderAgentRights = brand.HeaderPrefix + "Agent-Rights"
	// L'empreinte de la clé du serveur que l'agent a épinglée à son
	// enrôlement (KeyFingerprint), redite à chaque ouverture du flux : le
	// serveur la garde pour montrer qu'un agent ne vérifierait pas ses ordres.
	HeaderServerKey = brand.HeaderPrefix + "Server-Key"
)

const (
	// CodeRevoked : l'agent a prouvé sa clé, mais il est révoqué. Il
	// s'arrête.
	CodeRevoked = "revoked"
	// CodeLocalAgentActive : l'agent s'enrôle depuis la machine du serveur
	// par un jeton de machine neuve, et la machine openCloud a déjà un agent
	// actif. Le jeton n'est pas consommé.
	CodeLocalAgentActive = "local_agent_active"
)

type EnrollRequest struct {
	MachineID    string `json:"machine_id"`
	PublicKey    string `json:"public_key"`
	Token        string `json:"token"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agent_version"`
	// L'empreinte de la machine : celle du serveur fait de l'agent celui de
	// la machine openCloud. Absente chez un agent plus vieux ou sur une
	// machine sans identifiant : pas de rapprochement.
	HostFingerprint string `json:"host_fingerprint,omitempty"`
}

// La réponse à l'enrôlement : l'identifiant retenu pour la machine, son nom,
// l'agent inscrit, et la clé publique du serveur (base64), que l'agent
// épingle : le jeton, consommé par cette même requête, prouve que
// l'opérateur voulait cet enrôlement avec ce serveur.
type EnrollResponse struct {
	MachineID string `json:"machine_id"`
	Name      string `json:"name"`
	AgentID   string `json:"agent_id"`
	ServerKey string `json:"server_key"`
}

type ChallengeRequest struct {
	MachineID string `json:"machine_id"`
}

type ChallengeResponse struct {
	Nonce string `json:"nonce"`
}

// Ce que l'agent envoie avec son signal : les lectures de la machine faites
// depuis le signal précédent, ce que son Docker a montré, et ce que ses
// sondes ont donné, ce qu'il a constaté sur ses images, ce qu'il a fait des
// ordres reçus. Un signal sans
// corps reste un signal ; un agent plus vieux n'envoie pas toutes les
// sections, le serveur l'accepte.
type SignalRequest struct {
	Readings []sampler.Reading `json:"readings,omitempty"`
	// Clé figée : les agents installés l'envoient sous « services ».
	Services *container.Report `json:"services,omitempty"`
	Probes   *probe.Report     `json:"probes,omitempty"`
	Images   *update.Report    `json:"images,omitempty"`
	// Ce que l'agent a fait des ordres reçus : refusés, acceptés, faits.
	Actions *ActionReport `json:"actions,omitempty"`
}

// Les sections du signal, dans l'ordre où le serveur les écrit, et la clé
// rendue quand l'une est refusée. SectionServices garde son nom : clé
// figée, les agents installés l'envoient et lisent « bad_services ».
const (
	SectionReadings = "readings"
	SectionServices = "services"
	SectionProbes   = "probes"
	SectionImages   = "images"
	SectionActions  = "actions"
)

// SignalSections rend les sections dans l'ordre où le serveur les écrit.
func SignalSections() []string {
	return []string{SectionReadings, SectionServices, SectionProbes, SectionImages, SectionActions}
}

// SectionRefusal rend la clé d'erreur d'une section refusée.
func SectionRefusal(section string) string {
	return "bad_" + section
}

// La réponse à un signal dont une section au moins a été refusée, pour un
// agent qui a posé HeaderSignalSections : les autres sont écrites.
type SignalResponse struct {
	Rejected map[string]string `json:"rejected"`
}
