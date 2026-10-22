package server

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
)

// Les clés SSH autorisées d'une machine, telles que son agent les a relevées :
// des faits sur chaque clé publique (type, longueur, empreinte, commentaire,
// nom des options), rien du contenu d'un fichier. Le serveur ne juge pas une
// clé « faible » ; le front le déduit de la longueur et du type.
type sshKeyJSON struct {
	File        string   `json:"file"`
	Fingerprint string   `json:"fingerprint"`
	Type        string   `json:"type"`
	Bits        int      `json:"bits"`
	Comment     string   `json:"comment"`
	Options     []string `json:"options"`
}

type sshAccountJSON struct {
	Name      string       `json:"name"`
	UID       int64        `json:"uid"`
	State     string       `json:"state"`
	Truncated bool         `json:"truncated"`
	Ignored   int          `json:"ignored"`
	Keys      []sshKeyJSON `json:"keys"`
}

// Le relevé d'une machine. reported_at est nul tant que son agent n'en a
// envoyé aucun : un agent plus ancien n'en envoie pas.
type sshReportJSON struct {
	MachineID   string           `json:"machine_id"`
	MachineName string           `json:"machine_name"`
	ReportedAt  *time.Time       `json:"reported_at"`
	Truncated   bool             `json:"truncated"`
	Accounts    []sshAccountJSON `json:"accounts"`
}

// La vue « quelle clé ouvre quoi » : le relevé de chaque machine ; le front
// regroupe par empreinte.
type sshMatrixResponse struct {
	Machines []sshReportJSON `json:"machines"`
}

func (s *Server) getMachineSSHKeys(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.sshKeys.Machine(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, sshReportOf(snapshot))
}

func (s *Server) getSSHKeyMatrix(w http.ResponseWriter, r *http.Request) {
	snapshots, err := s.sshKeys.Matrix(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, sshMatrixResponse{Machines: httpx.ConvertAll(snapshots, sshReportOf)})
}

func sshReportOf(snapshot sshkeys.Snapshot) sshReportJSON {
	return sshReportJSON{
		MachineID:   snapshot.MachineID,
		MachineName: snapshot.MachineName,
		ReportedAt:  httpx.TimeOrNil(snapshot.ReportedAt),
		Truncated:   snapshot.Truncated,
		Accounts:    httpx.ConvertAll(snapshot.Accounts, sshAccountOf),
	}
}

func sshAccountOf(account sshkeys.Account) sshAccountJSON {
	return sshAccountJSON{
		Name:      account.Name,
		UID:       account.UID,
		State:     string(account.State),
		Truncated: account.Truncated,
		Ignored:   account.Ignored,
		Keys:      httpx.ConvertAll(account.Keys, sshKeyOf),
	}
}

func sshKeyOf(key sshkeys.Key) sshKeyJSON {
	return sshKeyJSON{
		File: key.File, Fingerprint: key.Fingerprint, Type: key.Type, Bits: key.Bits,
		Comment: key.Comment, Options: orEmpty(key.Options),
	}
}
