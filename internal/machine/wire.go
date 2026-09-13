package machine

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
