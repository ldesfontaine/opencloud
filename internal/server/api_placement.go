package server

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// La correction de l'opérateur : le type et l'hôte de la machine. Vides, ils
// retirent la correction.
type placementRequest struct {
	KindOverride string `json:"kind_override"`
	HostOverride string `json:"host_override"`
}

// setPlacement corrige où tourne la machine. L'agent ne la défait pas : son
// rapport suivant ne touche que le type qu'il a rapporté. Réservé à
// l'opérateur : la route n'est ni un verbe ni une lecture de jeton.
func (s *Server) setPlacement(w http.ResponseWriter, r *http.Request) {
	var request placementRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	kind, err := machine.ParseKind(request.KindOverride)
	if s.failed(w, r, err, nil) {
		return
	}
	updated, err := s.machines.SetPlacement(r.Context(), r.PathValue("id"), kind, request.HostOverride)
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	found, err := s.machines.Get(r.Context(), updated.ID)
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	facts, err := s.readMachineFacts(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, s.machineJSONOf(found, facts))
}

// Les disques physiques et les pools que l'agent a rapportés : vides tant qu'il
// n'a rien dit. Aucun relevé SMART.
type storageJSON struct {
	Disks []diskJSON `json:"disks"`
	Pools []poolJSON `json:"pools"`
}

type diskJSON struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	Size      int64  `json:"size"`
	Media     string `json:"media"`
	Removable bool   `json:"removable"`
}

type poolJSON struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Level   string   `json:"level"`
	State   string   `json:"state"`
	Size    int64    `json:"size"`
	Members []string `json:"members"`
}

func (s *Server) getMachineStorage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.machineExists(w, r, id) {
		return
	}
	storage, err := s.reader.MachineStorage(r.Context(), id)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, storageJSON{
		Disks: httpx.ConvertAll(storage.Disks, func(disk hostinfo.Disk) diskJSON {
			return diskJSON{Name: disk.Name, Model: disk.Model, Size: disk.Size, Media: disk.Media, Removable: disk.Removable}
		}),
		Pools: httpx.ConvertAll(storage.Pools, func(pool hostinfo.Pool) poolJSON {
			return poolJSON{Name: pool.Name, Kind: pool.Kind, Level: pool.Level, State: pool.State, Size: pool.Size, Members: orEmpty(pool.Members)}
		}),
	})
}
