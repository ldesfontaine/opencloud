package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

const (
	jobRunsShown  = 20
	jobPingsShown = 20
)

func (s *Server) jobsPage(w http.ResponseWriter, r *http.Request) {
	text := s.catalog()
	page, err := s.jobsPageData(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	data := s.newView(r, navJobs, text.Get("nav.jobs"), jobsSubtitle(text, page.Total, page.Attention))
	data.Page = page
	s.render(w, r, http.StatusOK, "jobs", data)
}

// Le tableau seul, rafraîchi par HTMX toutes les dix secondes.
func (s *Server) jobsTable(w http.ResponseWriter, r *http.Request) {
	page, err := s.jobsPageData(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	data := s.newView(r, navJobs, "", "")
	data.Page = page
	s.renderFragment(w, r, "jobs-table", data)
}

func (s *Server) newJobPage(w http.ResponseWriter, r *http.Request) {
	s.renderNewJob(w, r, http.StatusOK, newJobForm{IntervalMinutes: 60, GraceMinutes: 10})
}

// L'opérateur nomme la tâche et dit tous les combien elle doit pinger ; les
// minutes du formulaire deviennent des durées.
func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	form := newJobForm{
		Name:            r.FormValue("name"),
		MachineID:       r.FormValue("machine_id"),
		IntervalMinutes: formInt(r.FormValue("interval_minutes")),
		GraceMinutes:    formInt(r.FormValue("grace_minutes")),
	}
	if form.MachineID != "" {
		if _, err := s.machines.Get(r.Context(), form.MachineID); errors.Is(err, machine.ErrNotFound) {
			form.ErrorKey = "job.machine_invalid"
			s.renderNewJob(w, r, http.StatusUnprocessableEntity, form)
			return
		} else if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	created, err := s.heartbeats.Create(r.Context(), heartbeat.Definition{
		Name:      form.Name,
		MachineID: form.MachineID,
		Interval:  time.Duration(form.IntervalMinutes) * time.Minute,
		Grace:     time.Duration(form.GraceMinutes) * time.Minute,
	})
	if key, refused := jobRefusalKey(err); refused {
		form.ErrorKey = key
		s.renderNewJob(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.redirect(w, r, "/taches/"+created.ID)
}

func jobRefusalKey(err error) (string, bool) {
	switch {
	case errors.Is(err, heartbeat.ErrNameInvalid):
		return "job.name_invalid", true
	case errors.Is(err, heartbeat.ErrIntervalInvalid):
		return "job.interval_invalid", true
	case errors.Is(err, heartbeat.ErrGraceInvalid):
		return "job.grace_invalid", true
	}
	return "", false
}

func (s *Server) renderNewJob(w http.ResponseWriter, r *http.Request, status int, form newJobForm) {
	text := s.catalog()
	machines, err := s.machines.List(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, known := range machines {
		form.Machines = append(form.Machines, machineOption{ID: known.ID, Name: known.Name, Selected: known.ID == form.MachineID})
	}
	data := s.newView(r, navJobs, text.Get("job.new_title"), text.Get("job.new_subtitle"))
	data.Page = form
	s.render(w, r, status, "job-new", data)
}

// La page d'une tâche : son état, l'URL de ping et les extraits à coller,
// puis les dernières exécutions et les derniers pings.
func (s *Server) jobPage(w http.ResponseWriter, r *http.Request) {
	text := s.catalog()
	found, err := s.heartbeats.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, heartbeat.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	runs, err := s.heartbeats.Runs(r.Context(), found.ID, jobRunsShown)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	pings, err := s.heartbeats.Pings(r.Context(), found.ID, jobPingsShown)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	publicURL, isLocal := s.resolvePublicURL(r)
	data := s.newView(r, navJobs, found.Name, "")
	data.Page = jobPage{
		Job:      s.jobRow(text, found),
		PingURL:  publicURL + "/ping/" + found.Token,
		URLLocal: isLocal,
		Snippets: snippets(text, publicURL+"/ping/"+found.Token),
		Runs:     s.runRows(text, runs),
		Pings:    s.pingRows(text, pings),
	}
	s.render(w, r, http.StatusOK, "job", data)
}

// L'état seul, rafraîchi par HTMX : pastille et métadonnées.
func (s *Server) jobState(w http.ResponseWriter, r *http.Request) {
	found, err := s.heartbeats.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, heartbeat.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	data := s.newView(r, navJobs, "", "")
	data.Page = jobPage{Job: s.jobRow(s.catalog(), found)}
	s.renderFragment(w, r, "job-state", data)
}

func (s *Server) pauseJob(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	s.finishJobAction(w, r, s.heartbeats.Pause(r.Context(), r.PathValue("id")), "/taches/"+r.PathValue("id"))
}

func (s *Server) resumeJob(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	err := s.heartbeats.Resume(r.Context(), r.PathValue("id"))
	if errors.Is(err, heartbeat.ErrNotPaused) {
		s.refuse(w, r, http.StatusConflict, s.catalog().Get("job.not_paused"))
		return
	}
	s.finishJobAction(w, r, err, "/taches/"+r.PathValue("id"))
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	s.finishJobAction(w, r, s.heartbeats.Delete(r.Context(), r.PathValue("id")), "/taches")
}

func (s *Server) finishJobAction(w http.ResponseWriter, r *http.Request, err error, target string) {
	switch {
	case errors.Is(err, heartbeat.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.internalError(w, r, err)
	default:
		s.redirect(w, r, target)
	}
}

// Un champ numérique vide ou faux vaut zéro : c'est la validation du
// service qui le refuse, avec le bon message.
func formInt(value string) int {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}
