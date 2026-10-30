package agentwire

import (
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/role"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Ce que l'agent et le serveur échangent en JSON pour l'enrôlement ; les
// clés et octets voyagent en base64 standard. Les en-têtes portent la
// preuve signée qui ouvre le flux : l'aléa que l'agent a tiré, l'horodatage
// et la signature des deux (machine.SignedPayload).
const (
	// La version du protocole, posée par l'agent sur chaque requête. Le
	// serveur refuse un agent qui ne la pose pas, ou une autre :
	// 426 protocol_unsupported.
	HeaderProtocol     = brand.HeaderPrefix + "Agent-Protocol"
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
	// La racine des services que l'agent lit dans son fichier, redite à
	// l'ouverture du flux. Absente chez un agent plus vieux.
	HeaderAgentRoot = brand.HeaderPrefix + "Agent-Root"
	// Le compte sous lequel l'agent tourne, redit à chaque ouverture du flux,
	// « <uid>:<nom> » (machine.FormatAccount). Absent chez un agent plus
	// vieux : la fiche dit « pas encore rapporté ».
	HeaderAgentAccount = brand.HeaderPrefix + "Agent-Account"
	// Les droits cochés que le compte de l'agent ne peut pas exercer sur sa
	// machine, « <droit>=<raison> » séparés par « , » (machine.FormatUnavailable),
	// jugés avant chaque ouverture du flux. Absent quand tout est réalisable,
	// et chez un agent plus vieux.
	HeaderAgentUnavailable = brand.HeaderPrefix + "Agent-Unavailable"
	// La version du retour arrière que l'agent sait faire, un entier : posée
	// seulement s'il tourne sous le lanceur stable (brand.AgentGuardEnv). Absent
	// chez un agent plus vieux ou qui n'est pas migré : le serveur refuse
	// alors agent.update, jamais sans retour arrière.
	HeaderAgentRollback = brand.HeaderPrefix + "Agent-Rollback"
)

// RollbackVersion est la version du retour arrière que ce binaire sait faire
// avec le lanceur stable (le marqueur, le délai, le lien previous).
const RollbackVersion = 1

// ProtocolVersion est la seule que ce serveur et cet agent parlent. Le 1
// avait un défi émis par le serveur (/agent/challenge) ; le 2 le retire.
const ProtocolVersion = "2"

const (
	// CodeProtocolUnsupported : l'agent ne parle pas la version du serveur.
	// Il s'arrête : seul un binaire de la même version le ramène.
	CodeProtocolUnsupported = "protocol_unsupported"
	// CodeReplayed : cette preuve a déjà ouvert un flux. L'agent en signe
	// une autre, avec un aléa neuf.
	CodeReplayed = "replayed"
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

// Les bornes du flux serveur → agent : une ligne SSE, un événement. Un
// contenu long part en plusieurs lignes « data: » d'au plus EventDataLine
// octets, que l'agent recolle sans séparateur : le JSON du serveur ne porte
// jamais de saut de ligne, la découpe ne change rien à ce qu'il dit.
const (
	EventDataLine     = 32 << 10
	MaxEventLineBytes = 64 << 10
	MaxEventBytes     = 1 << 20
)

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
	// Les clés SSH autorisées de la machine, relevées en lecture seule : le
	// dernier relevé, envoyé quand il change et à chaque ouverture du flux.
	SSHKeys *sshkeys.Report `json:"ssh_keys,omitempty"`
	// Ce que l'agent reconnaît sur sa machine (un hôte Proxmox VE) : le
	// dernier rapport, envoyé quand il change et à chaque ouverture du flux.
	Roles *role.Report `json:"roles,omitempty"`
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
	SectionSSHKeys  = "ssh_keys"
	SectionRoles    = "roles"
)

// SignalSections rend les sections dans l'ordre où le serveur les écrit.
func SignalSections() []string {
	return []string{SectionReadings, SectionServices, SectionProbes, SectionImages, SectionActions, SectionSSHKeys, SectionRoles}
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
