package web

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Une machine telle que les gabarits la lisent : tout est déjà formaté.
type machineRow struct {
	ID           string
	Name         string
	Address      string
	OS           string
	AgentVersion string
	Local        bool
	Online       bool
	SeenAgo      string
}

type pendingRow struct {
	ID        string
	Name      string
	Masked    string
	ExpiresIn string
	Reenroll  bool
}

type machinesPage struct {
	Total   int
	Online  int
	Rows    []machineRow
	Pending []pendingRow
}

type overviewPage struct {
	Subtitle string
	Total    int
	Online   int
	Rows     []machineRow
}

type newMachineForm struct {
	Name     string
	ErrorKey string
}

type tokenPage struct {
	Name      string
	Token     string
	Command   string
	ExpiresIn string
	URLLocal  bool
}

type machineHeadData struct {
	Current machineRow
	All     []machineRow
}

type tabLink struct {
	Href   string
	Label  string
	Active bool
}

type summaryRow struct {
	Label string
	Value string
	Mono  bool
}

type machinePage struct {
	Head    machineHeadData
	Tab     string
	Tabs    []tabLink
	Summary []summaryRow
}

func (s *Server) machinesPageData(r *http.Request) (machinesPage, error) {
	text := s.catalog()
	statuses, err := s.machines.List(r.Context())
	if err != nil {
		return machinesPage{}, err
	}
	pending, err := s.machines.PendingTokens(r.Context())
	if err != nil {
		return machinesPage{}, err
	}
	page := machinesPage{Total: len(statuses)}
	for _, status := range statuses {
		if status.Online {
			page.Online++
		}
		page.Rows = append(page.Rows, s.row(text, status))
	}
	for _, token := range pending {
		page.Pending = append(page.Pending, pendingRow{
			ID:        token.ID,
			Name:      token.Name,
			Masked:    token.Masked(),
			ExpiresIn: formatDuration(text, token.ExpiresAt.Sub(s.now())),
			Reenroll:  token.MachineID != "",
		})
	}
	return page, nil
}

func (s *Server) overviewPage(text lang.Catalog, statuses []machine.Status) overviewPage {
	page := overviewPage{Total: len(statuses)}
	for _, status := range statuses {
		if status.Online {
			page.Online++
		}
		page.Rows = append(page.Rows, s.row(text, status))
	}
	page.Subtitle = countSubtitle(text, page.Total, page.Online)
	return page
}

func (s *Server) machineHeadData(r *http.Request, id string) (machineHeadData, error) {
	text := s.catalog()
	statuses, err := s.machines.List(r.Context())
	if err != nil {
		return machineHeadData{}, err
	}
	head := machineHeadData{}
	found := false
	for _, status := range statuses {
		row := s.row(text, status)
		head.All = append(head.All, row)
		if status.ID == id {
			head.Current = row
			found = true
		}
	}
	if !found {
		return machineHeadData{}, machine.ErrNotFound
	}
	return head, nil
}

func (s *Server) row(text lang.Catalog, status machine.Status) machineRow {
	return machineRow{
		ID:           status.ID,
		Name:         status.Name,
		Address:      status.Address,
		OS:           status.OS,
		AgentVersion: status.AgentVersion,
		Local:        status.IsLocal(),
		Online:       status.Online,
		SeenAgo:      s.seenAgo(text, status.LastSeenAt),
	}
}

func (s *Server) tabLinks(text lang.Catalog, id, active string) []tabLink {
	links := make([]tabLink, 0, len(machineTabs))
	for _, tab := range machineTabs {
		href := "/machines/" + id
		if tab.Slug != "" {
			href += "/" + tab.Slug
		}
		links = append(links, tabLink{Href: href, Label: text.Get(tab.Key), Active: tab.Slug == active})
	}
	return links
}

func (s *Server) summaryRows(text lang.Catalog, row machineRow) []summaryRow {
	kind := text.Get("machine.kind_remote")
	if row.Local {
		kind = text.Get("machine.kind_local")
	}
	return []summaryRow{
		{Label: text.Get("machine.field_kind"), Value: kind},
		{Label: text.Get("machine.field_address"), Value: row.Address, Mono: true},
		{Label: text.Get("machine.field_os"), Value: row.OS},
		{Label: text.Get("machine.field_agent"), Value: row.AgentVersion, Mono: true},
		{Label: text.Get("machine.field_seen"), Value: row.SeenAgo},
	}
}

// « vu il y a 12 s » ; jamais vu, ou à l'instant, ont leur propre mot.
func (s *Server) seenAgo(text lang.Catalog, at time.Time) string {
	if at.IsZero() {
		return text.Get("machine.never_seen")
	}
	since := s.now().Sub(at)
	if since < time.Second {
		return text.Get("machine.seen_now")
	}
	return text.Format("machine.seen_ago", formatDuration(text, since))
}

// formatDuration arrondit à l'unité qui se lit : secondes, minutes, heures,
// jours.
func formatDuration(text lang.Catalog, d time.Duration) string {
	switch {
	case d < 0:
		return formatDuration(text, 0)
	case d < time.Minute:
		return text.Format("time.seconds", int(d.Seconds()))
	case d < time.Hour:
		return text.Format("time.minutes", int(d.Minutes()))
	case d <= 24*time.Hour:
		return text.Format("time.hours", int(d.Hours()))
	default:
		return text.Format("time.days", int(d.Hours()/24))
	}
}

// Le sous-titre des pages qui comptent les machines : rien, une, plusieurs.
func countSubtitle(text lang.Catalog, total, online int) string {
	switch total {
	case 0:
		return text.Get("overview.subtitle")
	case 1:
		return text.Format("machines.subtitle_one", online)
	default:
		return text.Format("machines.subtitle", total, online)
	}
}
