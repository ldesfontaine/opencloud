package machine

import (
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Ce que l'agent et le serveur échangent en JSON pour l'enrôlement et le
// défi ; les clés et octets voyagent en base64 standard. Les en-têtes
// portent la preuve signée qui ouvre le flux.
const (
	HeaderMachine      = "OpenCloud-Machine"
	HeaderNonce        = "OpenCloud-Nonce"
	HeaderTimestamp    = "OpenCloud-Timestamp"
	HeaderSignature    = "OpenCloud-Signature"
	HeaderAgentVersion = "OpenCloud-Agent-Version"
	// L'empreinte de l'identifiant de la machine de l'agent : à
	// l'ouverture du flux, elle dit si un agent tourne sur la machine
	// openCloud elle-même.
	HeaderHostFingerprint = "OpenCloud-Host-Fingerprint"
	// Posé par un agent qui sait lire un refus par section ; sans lui, le
	// serveur répond comme avant, par un 400 global.
	HeaderSignalSections = "OpenCloud-Signal-Sections"
)

type EnrollRequest struct {
	MachineID    string `json:"machine_id"`
	PublicKey    string `json:"public_key"`
	Token        string `json:"token"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agent_version"`
	// Absente chez un agent plus vieux ou sur une machine sans
	// identifiant : pas de contrôle.
	HostFingerprint string `json:"host_fingerprint,omitempty"`
}

type EnrollResponse struct {
	MachineID string `json:"machine_id"`
	Name      string `json:"name"`
}

type ChallengeRequest struct {
	MachineID string `json:"machine_id"`
}

type ChallengeResponse struct {
	Nonce string `json:"nonce"`
}

// Ce que l'agent envoie avec son signal : les lectures de la machine faites
// depuis le signal précédent, ce que son Docker a montré, et ce que ses
// sondes ont donné, ce qu'il a constaté sur ses images. Un signal sans
// corps reste un signal ; un agent plus vieux n'envoie pas toutes les
// sections, le serveur l'accepte.
type SignalRequest struct {
	Readings []sampler.Reading `json:"readings,omitempty"`
	Services *service.Report   `json:"services,omitempty"`
	Probes   *probe.Report     `json:"probes,omitempty"`
	Images   *update.Report    `json:"images,omitempty"`
}

// Les sections du signal, dans l'ordre où le serveur les écrit, et la clé
// rendue quand l'une est refusée.
const (
	SectionReadings = "readings"
	SectionServices = "services"
	SectionProbes   = "probes"
	SectionImages   = "images"
)

// SignalSections rend les sections dans l'ordre où le serveur les écrit.
func SignalSections() []string {
	return []string{SectionReadings, SectionServices, SectionProbes, SectionImages}
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
