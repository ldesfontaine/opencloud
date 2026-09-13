package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Une tâche telle que les gabarits la lisent : tout est déjà formaté.
type jobRow struct {
	ID          string
	Name        string
	MachineID   string
	MachineName string
	Status      string
	StatusLabel string
	Tone        string
	Paused      bool
	Interval    string
	Grace       string
	LastPing    string
	Deadline    string
	LastResult  string
	ResultTone  string
}

type jobsPage struct {
	Total     int
	Attention int
	Rows      []jobRow
}

type machineOption struct {
	ID       string
	Name     string
	Selected bool
}

type newJobForm struct {
	Name            string
	MachineID       string
	IntervalMinutes int
	GraceMinutes    int
	Machines        []machineOption
	ErrorKey        string
}

type snippet struct {
	Key   string
	Label string
	Code  string
}

type runRow struct {
	Outcome  string
	Tone     string
	Duration string
	ExitCode string
	Started  string
	Payload  string
	When     string
}

// Ce qu'on montre du corps d'un ping : le début, sur une ligne.
const payloadPreview = 160

type pingRow struct {
	Kind     string
	ExitCode string
	Source   string
	Method   string
	When     string
}

type jobPage struct {
	Job      jobRow
	PingURL  string
	URLLocal bool
	Snippets []snippet
	Runs     []runRow
	Pings    []pingRow
}

func (s *Server) jobsPageData(r *http.Request) (jobsPage, error) {
	text := s.catalog()
	heartbeats, err := s.heartbeats.List(r.Context())
	if err != nil {
		return jobsPage{}, err
	}
	page := jobsPage{Total: len(heartbeats)}
	for _, found := range heartbeats {
		if heartbeat.NeedsAttention(found.Status) {
			page.Attention++
		}
		page.Rows = append(page.Rows, s.jobRow(text, found))
	}
	return page, nil
}

// Pastille par état : le vocabulaire de la direction artistique.
var jobTones = map[heartbeat.Status]string{
	heartbeat.StatusNew:     "neutral",
	heartbeat.StatusOnTime:  "ok",
	heartbeat.StatusStarted: "accent",
	heartbeat.StatusLate:    "danger",
	heartbeat.StatusFailed:  "danger",
	heartbeat.StatusPaused:  "neutral",
}

func (s *Server) jobRow(text lang.Catalog, found heartbeat.Heartbeat) jobRow {
	row := jobRow{
		ID:          found.ID,
		Name:        found.Name,
		MachineID:   found.MachineID,
		MachineName: found.MachineName,
		Status:      string(found.Status),
		StatusLabel: text.Get("job.status_" + string(found.Status)),
		Tone:        jobTones[found.Status],
		Paused:      found.IsPaused(),
		Interval:    formatDuration(text, found.Interval),
		Grace:       formatDuration(text, found.Grace),
		LastPing:    s.lastPing(text, found.LastPingAt),
		Deadline:    s.deadlineIn(text, found.NextDeadlineAt),
	}
	row.LastResult, row.ResultTone = s.lastResult(text, found)
	return row
}

// « il y a 2 min », ou « jamais » tant qu'aucun ping n'est arrivé.
func (s *Server) lastPing(text lang.Catalog, at time.Time) string {
	if at.IsZero() {
		return text.Get("job.never")
	}
	return s.agoOrNow(text, at)
}

// « dans 4 min », ou rien quand aucune échéance ne court.
func (s *Server) deadlineIn(text lang.Catalog, at time.Time) string {
	if at.IsZero() {
		return text.Get("job.no_deadline")
	}
	remaining := at.Sub(s.now())
	if remaining < 0 {
		return text.Get("job.deadline_passed")
	}
	return text.Format("job.deadline_in", formatDuration(text, remaining))
}

// Le dernier résultat connu : code de sortie et durée, quand la tâche les
// a donnés.
func (s *Server) lastResult(text lang.Catalog, found heartbeat.Heartbeat) (string, string) {
	if found.LastExitCode == nil && found.LastDuration == nil {
		return "", ""
	}
	result := ""
	tone := "ok"
	if found.LastExitCode != nil {
		result = text.Format("job.exit_code", *found.LastExitCode)
		if *found.LastExitCode != 0 {
			tone = "danger"
		}
	}
	if found.LastDuration != nil {
		if result != "" {
			result += " · "
		}
		result += formatDuration(text, *found.LastDuration)
	}
	return result, tone
}

var runTones = map[heartbeat.Outcome]string{
	heartbeat.OutcomeInProgress: "accent",
	heartbeat.OutcomeSuccess:    "ok",
	heartbeat.OutcomeFailure:    "danger",
	heartbeat.OutcomeTimeout:    "warn",
}

func (s *Server) runRows(text lang.Catalog, runs []heartbeat.Run) []runRow {
	rows := make([]runRow, 0, len(runs))
	for _, run := range runs {
		row := runRow{
			Outcome: text.Get("job.outcome_" + string(run.Outcome)),
			Tone:    runTones[run.Outcome],
			Payload: preview(run.Payload),
		}
		if run.CompletedAt.IsZero() {
			row.When = s.agoOrNow(text, run.StartedAt)
		} else {
			row.When = s.agoOrNow(text, run.CompletedAt)
		}
		if run.Duration != nil {
			row.Duration = formatDuration(text, *run.Duration)
		}
		if run.ExitCode != nil {
			row.ExitCode = text.Format("job.exit_code", *run.ExitCode)
		}
		if !run.StartedAt.IsZero() {
			row.Started = text.Format("job.started_at", formatClock(run.StartedAt))
		}
		rows = append(rows, row)
	}
	return rows
}

func preview(payload string) string {
	line, _, _ := strings.Cut(payload, "\n")
	runes := []rune(line)
	if len(runes) > payloadPreview {
		return string(runes[:payloadPreview]) + "…"
	}
	return line
}

func (s *Server) pingRows(text lang.Catalog, pings []heartbeat.Ping) []pingRow {
	rows := make([]pingRow, 0, len(pings))
	for _, ping := range pings {
		row := pingRow{
			Kind:   text.Get("job.kind_" + string(ping.Kind)),
			Source: ping.Source,
			Method: ping.Method,
			When:   formatClock(ping.ReceivedAt),
		}
		if ping.ExitCode != nil {
			row.ExitCode = strconv.Itoa(*ping.ExitCode)
		}
		rows = append(rows, row)
	}
	return rows
}

// « il y a 2 min » sans le verbe « vu ».
func (s *Server) agoOrNow(text lang.Catalog, at time.Time) string {
	since := s.now().Sub(at)
	if since < time.Second {
		return text.Get("job.just_now")
	}
	return text.Format("job.ago", formatDuration(text, since))
}

// Un instant en mono, lisible dans les deux langues, en heure du serveur.
func formatClock(at time.Time) string {
	return at.Format("2006-01-02 15:04:05")
}

// Les extraits à coller ; l'URL n'est construite qu'ici, par le serveur.
func snippets(text lang.Catalog, pingURL string) []snippet {
	curl := "curl -fsS -m 10 --retry 3 -o /dev/null "
	return []snippet{
		{Key: "curl", Label: text.Get("job.snippet_curl"), Code: curl + pingURL},
		{Key: "cron", Label: text.Get("job.snippet_cron"), Code: "0 2 * * * /usr/local/bin/sauvegarde.sh && " + curl + pingURL},
		{Key: "script", Label: text.Get("job.snippet_script"), Code: "#!/bin/bash\n" +
			curl + pingURL + "/start\n" +
			"/usr/local/bin/sauvegarde.sh\n" +
			"code=$?\n" +
			curl + pingURL + "/\"$code\"\n" +
			"exit \"$code\""},
		{Key: "docker", Label: text.Get("job.snippet_docker"), Code: "HEALTHCHECK --interval=5m CMD " + curl + pingURL + " || exit 1"},
	}
}

// Le sous-titre de la page des tâches : rien, ou le compte et ce qui
// demande attention.
func jobsSubtitle(text lang.Catalog, total, attention int) string {
	switch {
	case total == 0:
		return text.Get("jobs.subtitle_none")
	case total == 1 && attention == 0:
		return text.Get("jobs.subtitle_one")
	case total == 1:
		return text.Get("jobs.subtitle_one_attention")
	case attention == 0:
		return text.Format("jobs.subtitle_ok", total)
	default:
		return text.Format("jobs.subtitle_attention", total, attention)
	}
}
