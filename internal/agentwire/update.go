package agentwire

// AgentUpdateParams sont les paramètres de agent.update : la version visée,
// le colis où l'agent télécharge ses trois fichiers, et lequel est le binaire.
// Rien ici n'est un secret : le binaire est public et signé, et l'agent ne
// croit que sa propre clé de version, jamais ce que le serveur dit.
type AgentUpdateParams struct {
	Version string       `json:"version"`
	Parcel  string       `json:"parcel"`
	Binary  string       `json:"binary"`
	Files   []ParcelFile `json:"files"`
}
