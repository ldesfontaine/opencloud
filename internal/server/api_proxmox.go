package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// ownSnapshotRunsLimit borne la lecture des ordres d'où l'on tire les
// instantanés de ce produit : les plus récents suffisent, un instantané plus
// ancien se supprime à la main dans Proxmox.
const ownSnapshotRunsLimit = 500

// L'hôte Proxmox d'une machine, tel que la fiche le lit : ce que l'API de
// l'hyperviseur a dit au dernier relevé réussi. Le secret du jeton ne sort
// jamais, ni ici ni dans une erreur ; last_error est la clé du refus du
// dernier relevé qui a échoué, vide s'il a réussi.
type proxmoxHostJSON struct {
	APIURL     string     `json:"api_url"`
	TokenID    string     `json:"token_id"`
	Pin        string     `json:"pin"`
	Node       string     `json:"node"`
	Version    string     `json:"version"`
	VerifiedAt time.Time  `json:"verified_at"`
	LastReadAt *time.Time `json:"last_read_at"`
	LastError  string     `json:"last_error"`
	// Les VM et conteneurs par numéro, les stockages par nom.
	Guests   []proxmoxGuestJSON   `json:"guests"`
	Storages []proxmoxStorageJSON `json:"storages"`
}

type proxmoxGuestJSON struct {
	VMID     int    `json:"vmid"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Template bool   `json:"template"`
	Node     string `json:"node"`
	// Ce qui est alloué, pas ce qui est consommé.
	CPUs      float64 `json:"cpus"`
	MaxMemory int64   `json:"max_memory"`
	MaxDisk   int64   `json:"max_disk"`
	// La machine enrôlée qui porte ce nom d'hôte, null sans rapprochement.
	LinkedMachineID *string    `json:"linked_machine_id"`
	LastBackupAt    *time.Time `json:"last_backup_at"`
	LastBackupBytes int64      `json:"last_backup_bytes"`
	// Les instantanés de ce produit que ses ordres ont créés et que nul n'a
	// supprimés depuis : ce que le menu de l'invité propose de supprimer.
	OwnSnapshots []string `json:"own_snapshots"`
}

type proxmoxStorageJSON struct {
	Node    string   `json:"node"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Total   int64    `json:"total"`
	Used    int64    `json:"used"`
	Avail   int64    `json:"avail"`
	Content []string `json:"content"`
	Active  bool     `json:"active"`
}

// Ce que chaque machine dit de Proxmox : porte-t-elle un hôte, et, si elle
// est la VM d'un hôte, laquelle (hosted).
type proxmoxSummaryJSON struct {
	Declared  bool               `json:"declared"`
	Guests    int                `json:"guests"`
	LastError string             `json:"last_error"`
	Hosted    *proxmoxHostedJSON `json:"hosted"`
}

type proxmoxHostedJSON struct {
	HostMachineID   string     `json:"host_machine_id"`
	HostMachineName string     `json:"host_machine_name"`
	VMID            int        `json:"vmid"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	CPUs            float64    `json:"cpus"`
	MaxMemory       int64      `json:"max_memory"`
	LastBackupAt    *time.Time `json:"last_backup_at"`
	LastBackupBytes int64      `json:"last_backup_bytes"`
}

// Le secret arrive ici et nulle part ailleurs : le composant le voit le temps
// de joindre l'API, la base le garde scellé, rien ne le journalise.
type proxmoxRequest struct {
	APIURL      string `json:"api_url"`
	TokenID     string `json:"token_id"`
	TokenSecret string `json:"token_secret"`
	// Facultative : l'empreinte SHA-256 du certificat de l'hôte.
	Pin string `json:"pin"`
}

func proxmoxViewToJSON(view proxmox.View, runs []action.Run) proxmoxHostJSON {
	host := view.Host
	return proxmoxHostJSON{
		APIURL: host.APIURL, TokenID: host.TokenID, Pin: host.Pin, Node: host.Node, Version: host.Version,
		VerifiedAt: host.VerifiedAt.UTC(), LastReadAt: httpx.TimeOrNil(host.LastReadAt), LastError: host.LastError,
		Guests: httpx.ConvertAll(view.Guests, func(guest proxmox.Guest) proxmoxGuestJSON {
			return proxmoxGuestToJSON(guest, proxmox.OwnSnapshots(runs, guest.VMID))
		}),
		Storages: httpx.ConvertAll(view.Storages, proxmoxStorageToJSON),
	}
}

func proxmoxGuestToJSON(guest proxmox.Guest, ownSnapshots []string) proxmoxGuestJSON {
	converted := proxmoxGuestJSON{
		VMID: guest.VMID, Kind: guest.Kind, Name: guest.Name, Status: guest.Status, Template: guest.Template, Node: guest.Node,
		CPUs: guest.CPUs, MaxMemory: guest.MaxMem, MaxDisk: guest.MaxDisk,
		LastBackupAt: httpx.TimeOrNil(guest.LastBackupAt), LastBackupBytes: guest.LastBackupBytes,
		OwnSnapshots: orEmpty(ownSnapshots),
	}
	if guest.LinkedMachineID != "" {
		converted.LinkedMachineID = &guest.LinkedMachineID
	}
	return converted
}

func proxmoxStorageToJSON(storage proxmox.Storage) proxmoxStorageJSON {
	return proxmoxStorageJSON{
		Node: storage.Node, Name: storage.Name, Type: storage.Type, Total: storage.Total, Used: storage.Used, Avail: storage.Avail,
		Content: orEmpty(storage.Content), Active: storage.Active,
	}
}

func proxmoxSummaryToJSON(summary proxmox.Summary) proxmoxSummaryJSON {
	converted := proxmoxSummaryJSON{Declared: summary.Declared, Guests: summary.Guests, LastError: summary.LastError}
	if summary.Hosted != nil {
		hosted := summary.Hosted
		converted.Hosted = &proxmoxHostedJSON{
			HostMachineID: hosted.HostMachineID, HostMachineName: hosted.HostName, VMID: hosted.VMID, Kind: hosted.Kind,
			Name: hosted.Name, Status: hosted.Status, CPUs: hosted.CPUs, MaxMemory: hosted.MaxMem,
			LastBackupAt: httpx.TimeOrNil(hosted.LastBackupAt), LastBackupBytes: hosted.LastBackupBytes,
		}
	}
	return converted
}

// proxmoxFailed répond à l'erreur d'un geste sur l'hôte : une machine inconnue
// et un hôte non déclaré sont, l'un comme l'autre, un 404.
func (s *Server) proxmoxFailed(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, machine.ErrNotFound) {
		return s.failed(w, r, err, machine.ErrNotFound)
	}
	return s.failed(w, r, err, proxmox.ErrNotFound)
}

func (s *Server) getMachineProxmox(w http.ResponseWriter, r *http.Request) {
	s.writeProxmoxView(w, r, http.StatusOK, r.PathValue("id"))
}

// writeProxmoxView répond la fiche de l'hôte : le dernier relevé, et les
// instantanés que les ordres de ce produit y ont laissés.
func (s *Server) writeProxmoxView(w http.ResponseWriter, r *http.Request, status int, machineID string) {
	view, err := s.reader.GetProxmoxView(r.Context(), machineID)
	if s.proxmoxFailed(w, r, err) {
		return
	}
	runs, err := s.reader.ListActionRuns(r.Context(), action.Query{
		MachineID: machineID, Kinds: []agentwire.Kind{agentwire.KindProxmoxSnapshot, agentwire.KindProxmoxSnapshotDelete},
		Limit: ownSnapshotRunsLimit,
	})
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, status, proxmoxViewToJSON(view, runs))
}

// Déclarer ou redéclarer l'hôte : le jeton répond à l'API avant que rien ne
// s'écrive, puis un premier relevé part. La réponse est la fiche de l'hôte,
// avec sa dernière erreur si ce relevé a échoué.
func (s *Server) setMachineProxmox(w http.ResponseWriter, r *http.Request) {
	var request proxmoxRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	id := r.PathValue("id")
	err := s.proxmox.Declare(r.Context(), id, proxmox.Definition{
		APIURL: request.APIURL, TokenID: request.TokenID, TokenSecret: request.TokenSecret, Pin: request.Pin,
	})
	if s.proxmoxFailed(w, r, err) {
		return
	}
	s.writeProxmoxView(w, r, http.StatusOK, id)
}

// Une demande de geste sur un invité : le seul paramètre est le nom de
// l'instantané à supprimer. Le nom d'un instantané créé n'est jamais saisi.
type proxmoxGuestActionRequest struct {
	Snapshot string `json:"snapshot"`
}

// actOnProxmoxGuest envoie le geste à l'agent de l'hôte : le serveur juge
// d'abord (invité connu, pas un modèle, état cohérent, jamais l'arrêt de la
// machine qui l'héberge), écrit le run et l'audit, puis l'ordre part. La
// réponse est le run ; l'issue réelle vient de l'agent, par le signal.
func (s *Server) actOnProxmoxGuest(verb proxmox.Verb) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vmid, err := strconv.Atoi(r.PathValue("vmid"))
		if err != nil {
			s.apiNotFound(w)
			return
		}
		var request proxmoxGuestActionRequest
		if !s.readAPI(w, r, &request) {
			return
		}
		run, err := s.proxmox.Act(r.Context(), r.PathValue("id"), vmid, verb, request.Snapshot)
		if s.proxmoxFailed(w, r, err) {
			return
		}
		s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
	}
}

func (s *Server) removeMachineProxmox(w http.ResponseWriter, r *http.Request) {
	err := s.proxmox.Remove(r.Context(), r.PathValue("id"))
	if s.proxmoxFailed(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
