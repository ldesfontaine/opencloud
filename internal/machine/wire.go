package machine

import (
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/service"
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
)

type EnrollRequest struct {
	MachineID    string `json:"machine_id"`
	PublicKey    string `json:"public_key"`
	Token        string `json:"token"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agent_version"`
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
// depuis le signal précédent, et ce que son Docker a montré. Un signal
// sans corps reste un signal ; un agent plus vieux n'envoie pas de
// services, le serveur l'accepte.
type SignalRequest struct {
	Readings []sampler.Reading `json:"readings,omitempty"`
	Services *service.Report   `json:"services,omitempty"`
}
