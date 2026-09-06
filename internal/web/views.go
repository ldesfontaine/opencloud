package web

import (
	"context"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// Combien d'actions passées la fiche d'une machine montre.
const machineHistoryLimit = 20

// Assez d'actions récentes pour voir qu'une action tourne sur la machine, pas
// assez pour relire tout son historique à chaque affichage de l'Infrastructure.
const machineRecentLimit = 5

type infrastructureView struct {
	Machines []machineRow
	CanEnrol bool
}

type machineRow struct {
	ID             string
	Name           string
	Address        string
	Enrolled       bool
	EnrolmentLabel string
	Status         machineStatus
	LastAction     *actionRow
}

type actionRow struct {
	ID         string
	Label      string
	State      string
	StateLabel string
	CreatedAt  string
	Result     string
}

type machineView struct {
	ID             string
	Name           string
	Address        string
	Port           int
	Account        string
	Enrolled       bool
	EnrolmentLabel string
	Status         machineStatus
	// La commande à coller sur la machine, vide dès qu'elle est enrôlée et
	// pour la machine openCloud, qui s'amorce par enroll-local.
	Command         string
	CanProbe        bool
	CanChangeAccess bool
	Refusal         *refusalView
	Available       []availableAction
	History         []actionRow
}

// machineFormView : déclarer une machine, premier temps de l'enrôlement.
type machineFormView struct {
	Values  map[string]string
	Refusal *refusalView
}

type availableAction struct {
	Kind    string
	Label   string
	Summary string
}

// attribute : un des quatre attributs d'une action, en clair.
type attribute struct {
	Label string
	Value string
}

type preparedFile struct {
	Path string
	Size string
	Mode string
}

type refusalView struct {
	Cause  string
	Remedy string
}

func newRefusalView(refused refusal.Refusal) refusalView {
	return refusalView{Cause: refused.Cause, Remedy: refused.Remedy}
}

type actionFormView struct {
	Machine           machineRow
	Kind              string
	Label             string
	Summary           string
	Attributes        []attribute
	Params            []catalog.ParamSpec
	Values            map[string]string
	Files             []preparedFile
	NeedsConfirmation bool
	Refusal           *refusalView
}

type actionView struct {
	ID         string
	Machine    machineRow
	Label      string
	Kind       string
	State      string
	StateLabel string
	Attributes []attribute
	Result     string
	Note       string
	ExitCode   string
	CreatedAt  string
	LaunchedAt string
	FinishedAt string
	Lines      []store.ActionLine
	Running    bool
	StreamPath string
}

// Les quatre attributs que l'écran montre avant d'exécuter (05-execution.md).
func attributesOf(definition catalog.Definition) []attribute {
	return []attribute{
		{Label: "Portée", Value: scopeLabel(definition.Scope)},
		{Label: "Lieu", Value: placeLabel(definition.Place)},
		{Label: "Réversibilité", Value: reversibilityLabel(definition.Reversible)},
		{Label: "Interruption", Value: interruptionLabel(definition.Interrupts)},
	}
}

// enrolmentStatus lit l'enrôlement. Sans dépendance branchée, aucune machine
// n'est enrôlée — c'est l'état vrai tant que enroll-local n'existe pas.
func (s *Server) enrolmentStatus(ctx context.Context, machineID string) EnrolmentStatus {
	if s.enrolment == nil {
		return EnrolmentStatus{}
	}
	status, err := s.enrolment.Status(ctx, machineID)
	if err != nil {
		s.logger.Warn("read enrolment status", "machine", machineID, "error", err)
		return EnrolmentStatus{}
	}
	return status
}

func newMachineRow(machine store.Machine, status EnrolmentStatus) machineRow {
	return machineRow{
		ID:             machine.ID,
		Name:           machine.Name,
		Address:        machine.Address,
		Enrolled:       status.Enrolled,
		EnrolmentLabel: enrolmentLabel(machine, status),
	}
}

func (s *Server) newActionRow(action store.Action) actionRow {
	label := action.Kind
	if definition, found := s.catalog.Lookup(catalog.Kind(action.Kind)); found {
		label = definition.Label
	}
	return actionRow{
		ID:         action.ID,
		Label:      label,
		State:      string(action.State),
		StateLabel: actionStateLabel(action.State),
		CreatedAt:  formatMoment(action.CreatedAt),
		Result:     action.Result,
	}
}

func (s *Server) newActionRows(actions []store.Action) []actionRow {
	var rows []actionRow
	for _, action := range actions {
		rows = append(rows, s.newActionRow(action))
	}
	return rows
}

func describeFiles(files []catalog.File) []preparedFile {
	var described []preparedFile
	for _, file := range files {
		described = append(described, preparedFile{
			Path: file.Path,
			Size: fmt.Sprintf("%d octets", len(file.Content)),
			Mode: fmt.Sprintf("%04o", file.Mode.Perm()),
		})
	}
	return described
}

func formatMoment(moment time.Time) string {
	if moment.IsZero() {
		return ""
	}
	return moment.Local().Format("02/01/2006 à 15:04:05")
}
