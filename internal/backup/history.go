package backup

import (
	"strconv"
	"time"
)

// Result est l'issue d'une cible d'un passage : reçue, en échec (l'ordre est
// parti, quelque chose a manqué), ou refusée avant tout ordre.
type Result string

const (
	ResultOK      Result = "ok"
	ResultFailed  Result = "failed"
	ResultRefused Result = "refused"
)

const (
	// MaxRunsPerPlan borne l'historique d'un plan : au-delà, les lignes les
	// plus anciennes partent à l'écriture de la suivante.
	MaxRunsPerPlan = 400

	DefaultRunsLimit = 50
)

// SourceDatabaseKey est la clé de la source « base d'openCloud » ; vide dans
// l'historique, la source dit que le plan entier est refusé avant toute cible.
const SourceDatabaseKey = "database"

// GuestSourceKey nomme une VM ou un conteneur d'un hôte Proxmox : la machine de
// l'hôte et le numéro, les deux dont le nom du fichier vzdump ne se passe pas.
func GuestSourceKey(hostMachineID string, vmid int) string {
	return "guest:" + hostMachineID + ":" + strconv.Itoa(vmid)
}

// VolumeSourceKey nomme un volume Docker d'une machine.
func VolumeSourceKey(machineID, name string) string {
	return "volume:" + machineID + ":" + name
}

// RunRecord est une ligne de l'historique : une cible d'un passage d'un plan.
type RunRecord struct {
	ID      int64
	Subject string
	// PassAt est l'instant de la tentative du plan : les cibles d'un passage le
	// partagent.
	PassAt     time.Time
	Source     string
	StartedAt  time.Time
	FinishedAt time.Time
	Result     Result
	// Un mot fermé, ou la clé du refus ; vide pour une cible reçue sans souci.
	Reason string
	// Nil tant que la taille de l'archive n'est pas connue.
	SizeBytes       *int64
	DestinationID   string
	DestinationName string
	ActionRunID     string
}

// RunQuery filtre l'historique : un plan, une source, les plus récentes
// d'abord. Limit zéro vaut DefaultRunsLimit.
type RunQuery struct {
	Subject string
	Source  string
	Limit   int
}

// PassCounts sont les cibles du dernier passage d'un plan.
type PassCounts struct {
	OK     int
	Failed int
}
