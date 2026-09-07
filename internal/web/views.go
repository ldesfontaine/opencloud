package web

import (
	"context"
	"fmt"
	"strings"
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

// Ce que la carte « En cours » montre de la sortie : de quoi voir où en est
// l'action, pas de quoi relire son journal.
const runningLinesShown = 3

// Les quatre compteurs de l'Infrastructure. La valeur est aussi la classe de
// la carte.
const (
	counterReachable   = "reachable"
	counterRunning     = "running"
	counterFailed      = "failed"
	counterNotEnrolled = "not-enrolled"
)

type infrastructureView struct {
	Machines []machineRow
	Counters []machineCounter
	Subtitle string
	CanEnrol bool
}

// machineCounter : un des quatre compteurs en tête de l'Infrastructure. Kind
// est aussi la classe de la carte.
type machineCounter struct {
	Kind  string
	Count int
	Label string
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
	// Ce qui tourne en ce moment sur la machine, nil quand rien ne tourne.
	Running *runningAction
}

// runningAction : l'action en cours sur une machine, et ses dernières lignes
// de sortie — de quoi voir où elle en est sans ouvrir sa page.
type runningAction struct {
	ID         string
	Label      string
	State      string
	StateLabel string
	CreatedAt  string
	LaunchedAt string
	Lines      []outputLine
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
	Lines      []outputLine
	Running    bool
	StreamPath string
}

// outputLine : une ligne de sortie et la classe qui lui donne sa couleur. Le
// préfixe est celui qu'écrivent les scripts (17-conventions-code.md).
type outputLine struct {
	Class string
	Text  string
}

// Les classes d'une ligne de sortie, dans l'ordre où on les reconnaît.
const (
	lineStep    = "step"
	lineWarning = "warning"
	lineResult  = "result"
	linePlain   = "plain"
)

func newOutputLines(lines []store.ActionLine) []outputLine {
	var rendered []outputLine
	for _, line := range lines {
		rendered = append(rendered, outputLine{Class: outputLineClass(line.Text), Text: line.Text})
	}
	return rendered
}

// Le CSS ne sait pas lire un préfixe : la classe se pose ici, et actions.js
// pose la même sur les lignes qui arrivent en direct.
func outputLineClass(text string) string {
	switch {
	case strings.HasPrefix(text, prefixStep):
		return lineStep
	case strings.HasPrefix(text, prefixWarning):
		return lineWarning
	case strings.HasPrefix(text, prefixResult):
		return lineResult
	}
	return linePlain
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
