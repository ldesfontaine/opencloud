package server

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/manifest"
)

const manifestFileName = brand.ID + ".yaml"

// exportedDirectory écrit l'annuaire, commentaires dans la langue de
// l'interface : le paquet manifest n'importe pas les catalogues, il reçoit
// ses textes d'ici.
func (s *Server) exportedDirectory(ctx context.Context, options manifest.ExportOptions) ([]byte, error) {
	code, err := s.language(ctx)
	if err != nil {
		return nil, err
	}
	catalog := s.catalogs.For(code)
	return s.manifest.Export(ctx, manifest.Annotations{
		Header:       catalog.Get("manifest.export_header"),
		NeedsReview:  catalog.Get("manifest.export_needs_review"),
		Proposed:     catalog.Get("manifest.export_proposed"),
		ProposedUnit: catalog.Get("manifest.export_proposed_unit"),
	}, options)
}

// exportManifest sert le fichier tel quel, en téléchargement : le même
// chemin pour le bouton de l'interface et pour la commande locale. Seul
// « ?propose=1 » ajoute des propositions ; toute autre valeur, ou son absence,
// sert l'annuaire seul.
func (s *Server) exportManifest(w http.ResponseWriter, r *http.Request) {
	options := manifest.ExportOptions{Propose: r.URL.Query().Get("propose") == "1"}
	data, err := s.exportedDirectory(r.Context(), options)
	if errors.Is(err, manifest.ErrExportTooLarge) {
		s.apiRefuse(w, http.StatusUnprocessableEntity, manifest.KeyExportTooLarge)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+manifestFileName+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(data); err != nil {
		s.logger.Warn("write manifest export", "error", err)
	}
}

// maxManifestRequest borne le corps d'un plan ou d'un apply : le fichier
// (256 Kio au plus) dans une chaîne JSON, échappements compris.
const maxManifestRequest = 1 << 20

// Les noms que le journal garde pour l'acteur et pour le fichier : indicatifs,
// donc bornés et sans caractère de contrôle.
const (
	maxActorNameRunes = 64
	maxFileNameRunes  = 128
)

type manifestRequest struct {
	File     string `json:"file"`
	FileName string `json:"file_name"`
	Prune    bool   `json:"prune"`
	PlanHash string `json:"plan_hash"`
	// ActorName n'est lu que sur la socket : la commande y déclare le compte
	// système qui la lance. Sur TCP, l'identité vient de l'authentification.
	ActorName string `json:"actor_name"`
}

type planSummaryJSON struct {
	Create    int `json:"create"`
	Update    int `json:"update"`
	Delete    int `json:"delete"`
	Unchanged int `json:"unchanged"`
	Unmanaged int `json:"unmanaged"`
}

type fieldChangeJSON struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type carryJSON struct {
	What  string `json:"what"`
	Count int    `json:"count"`
}

type changeJSON struct {
	Action  string            `json:"action"`
	Kind    string            `json:"kind"`
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Fields  []fieldChangeJSON `json:"fields"`
	Notes   []string          `json:"notes"`
	Carries []carryJSON       `json:"carries"`
}

type refJSON struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type problemJSON struct {
	Key    string `json:"key"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Path   string `json:"path"`
	Value  string `json:"value"`
}

type planJSON struct {
	Language   string          `json:"language"`
	FileHash   string          `json:"file_hash"`
	Prune      bool            `json:"prune"`
	PlanHash   string          `json:"plan_hash"`
	Applicable bool            `json:"applicable"`
	Summary    planSummaryJSON `json:"summary"`
	Changes    []changeJSON    `json:"changes"`
	Unmanaged  []refJSON       `json:"unmanaged"`
	Problems   []problemJSON   `json:"problems"`
}

type applySummaryJSON struct {
	Create    int `json:"create"`
	Update    int `json:"update"`
	Delete    int `json:"delete"`
	Unchanged int `json:"unchanged"`
}

type applyJSON struct {
	ID          string           `json:"id"`
	At          time.Time        `json:"at"`
	FinishedAt  *time.Time       `json:"finished_at"`
	ActorKind   string           `json:"actor_kind"`
	ActorName   string           `json:"actor_name"`
	FileHash    string           `json:"file_hash"`
	FileName    string           `json:"file_name"`
	Prune       bool             `json:"prune"`
	PlanHash    string           `json:"plan_hash"`
	State       string           `json:"state"`
	Summary     applySummaryJSON `json:"summary"`
	Done        int              `json:"done"`
	Error       string           `json:"error"`
	ErrorObject string           `json:"error_object"`
	Changes     []changeJSON     `json:"changes,omitempty"`
}

type appliesResponse struct {
	Applies []applyJSON `json:"applies"`
}

// manifestRefusalJSON est un refus qui porte le plan d'aujourd'hui : la
// commande et l'interface le montrent sans le redemander.
type manifestRefusalJSON struct {
	Error string   `json:"error"`
	Plan  planJSON `json:"plan"`
}

func changesToJSON(changes []manifest.Change) []changeJSON {
	found := make([]changeJSON, 0, len(changes))
	for _, change := range changes {
		item := changeJSON{
			Action: string(change.Action), Kind: string(change.Kind), ID: change.ID, Name: change.Name,
			Fields: make([]fieldChangeJSON, 0, len(change.Fields)), Notes: make([]string, 0, len(change.Notes)),
			Carries: make([]carryJSON, 0, len(change.Carries)),
		}
		for _, field := range change.Fields {
			item.Fields = append(item.Fields, fieldChangeJSON{Field: field.Field, Before: field.Before, After: field.After})
		}
		for _, note := range change.Notes {
			item.Notes = append(item.Notes, string(note))
		}
		for _, carry := range change.Carries {
			item.Carries = append(item.Carries, carryJSON{What: carry.What, Count: carry.Count})
		}
		found = append(found, item)
	}
	return found
}

func (s *Server) planToJSON(ctx context.Context, plan manifest.Plan) (planJSON, error) {
	code, err := s.language(ctx)
	if err != nil {
		return planJSON{}, err
	}
	created, updated, deleted := plan.Counts()
	response := planJSON{
		Language: string(code), FileHash: plan.FileHash, Prune: plan.Prune, PlanHash: plan.Hash,
		Applicable: plan.Applicable(),
		Summary: planSummaryJSON{
			Create: created, Update: updated, Delete: deleted, Unchanged: plan.Unchanged, Unmanaged: len(plan.Unmanaged),
		},
		Changes:   changesToJSON(plan.Changes),
		Unmanaged: convertAll(plan.Unmanaged, func(ref manifest.Ref) refJSON { return refJSON{Kind: string(ref.Kind), ID: ref.ID, Name: ref.Name} }),
		Problems:  make([]problemJSON, 0, len(plan.Problems)),
	}
	for _, problem := range plan.Problems {
		response.Problems = append(response.Problems, problemJSON{
			Key: problem.Key, Line: problem.Line, Column: problem.Column, Path: problem.Path, Value: problem.Value,
		})
	}
	return response, nil
}

func applyToJSON(found manifest.Apply, withChanges bool) applyJSON {
	response := applyJSON{
		ID: found.ID, At: found.At.UTC(), ActorKind: string(found.Actor.Kind), ActorName: found.Actor.Name,
		FileHash: found.FileHash, FileName: found.FileName, Prune: found.Prune, PlanHash: found.PlanHash,
		State:   string(found.State),
		Summary: applySummaryJSON{Create: found.Created, Update: found.Updated, Delete: found.Deleted, Unchanged: found.Unchanged},
		Done:    found.Done, Error: found.Error, ErrorObject: found.ErrorObject,
		FinishedAt: timeOrNil(found.FinishedAt),
	}
	if withChanges {
		response.Changes = changesToJSON(found.Changes)
	}
	return response
}

// controlSocketKey marque dans le contexte une requête venue de la socket de
// commande : l'acteur est alors la commande locale.
type controlSocketKey struct{}

func fromControlSocket(ctx context.Context) bool {
	_, onSocket := ctx.Value(controlSocketKey{}).(struct{})
	return onSocket
}

// printable borne un nom indicatif : sans caractère de contrôle, coupé.
func printable(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, text)
	if runes := []rune(text); len(runes) > limit {
		text = string(runes[:limit])
	}
	return strings.TrimSpace(text)
}

// actorOf dit qui lance l'apply. Sur la socket : la commande, sous le nom
// qu'elle déclare. Sur TCP : l'opérateur, sous l'identité que
// l'authentification a vue (Basic ou en-tête transmis), vide sans
// authentification. Ce nom est indicatif, pas une preuve.
func actorOf(r *http.Request, request manifestRequest) manifest.Actor {
	if fromControlSocket(r.Context()) {
		return manifest.Actor{Kind: manifest.ActorCLI, Name: printable(request.ActorName, maxActorNameRunes)}
	}
	name := identityFrom(r.Context())
	if name == "" {
		name, _, _ = r.BasicAuth()
	}
	return manifest.Actor{Kind: manifest.ActorOperator, Name: printable(name, maxActorNameRunes)}
}

// readManifestRequest lit le fichier à juger. Un corps qui n'en est pas un
// est un refus de la requête, pas du fichier.
func (s *Server) readManifestRequest(w http.ResponseWriter, r *http.Request) (manifestRequest, bool) {
	var request manifestRequest
	if !readBounded(w, r, &request, maxManifestRequest) {
		s.apiRefuse(w, http.StatusUnprocessableEntity, manifest.KeyRequestInvalid)
		return manifestRequest{}, false
	}
	request.FileName = printable(path.Base(strings.ReplaceAll(request.FileName, "\\", "/")), maxFileNameRunes)
	if request.FileName == "." || request.FileName == "/" {
		request.FileName = ""
	}
	return request, true
}

func (request manifestRequest) toRequest() manifest.Request {
	return manifest.Request{File: []byte(request.File), FileName: request.FileName, Prune: request.Prune, PlanHash: request.PlanHash}
}

// planManifest juge un fichier sans rien écrire. Un fichier refusé n'est pas
// une panne : la réponse est un 200 qui dit applicable:false et pourquoi.
func (s *Server) planManifest(w http.ResponseWriter, r *http.Request) {
	request, ok := s.readManifestRequest(w, r)
	if !ok {
		return
	}
	plan, err := s.manifest.Plan(r.Context(), request.toRequest())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response, err := s.planToJSON(r.Context(), plan)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

// applyManifest écrit le fichier si son plan est celui que l'opérateur a lu.
func (s *Server) applyManifest(w http.ResponseWriter, r *http.Request) {
	request, ok := s.readManifestRequest(w, r)
	if !ok {
		return
	}
	applied, plan, err := s.manifest.Apply(r.Context(), request.toRequest(), actorOf(r, request))
	switch {
	case errors.Is(err, manifest.ErrBusy):
		s.apiRefuse(w, http.StatusConflict, manifest.KeyBusy)
	case errors.Is(err, manifest.ErrNothingToApply):
		s.apiRefuse(w, http.StatusUnprocessableEntity, manifest.KeyNothingToApply)
	case errors.Is(err, manifest.ErrPlanChanged):
		s.refuseWithPlan(w, r, http.StatusConflict, manifest.KeyPlanChanged, plan)
	case errors.Is(err, manifest.ErrNotApplicable):
		s.refuseWithPlan(w, r, http.StatusUnprocessableEntity, manifest.KeyNotApplicable, plan)
	case err != nil:
		s.apiInternalError(w, r, err)
	default:
		s.writeAPI(w, http.StatusCreated, applyToJSON(applied, true))
	}
}

func (s *Server) refuseWithPlan(w http.ResponseWriter, r *http.Request, status int, key string, plan manifest.Plan) {
	response, err := s.planToJSON(r.Context(), plan)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, status, manifestRefusalJSON{Error: key, Plan: response})
}

// Le journal garde les quarante derniers passages par défaut, et au plus deux
// cents par lecture.
const (
	defaultAppliesLimit = 40
	maxAppliesLimit     = 200
)

func (s *Server) listApplies(w http.ResponseWriter, r *http.Request) {
	limit := defaultAppliesLimit
	if text := r.URL.Query().Get("limit"); text != "" {
		parsed, err := strconv.Atoi(text)
		if err != nil || parsed < 1 || parsed > maxAppliesLimit {
			s.apiRefuse(w, http.StatusUnprocessableEntity, manifest.KeyRequestInvalid)
			return
		}
		limit = parsed
	}
	found, err := s.manifest.Applies(r.Context(), limit)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := appliesResponse{Applies: convertAll(found, func(item manifest.Apply) applyJSON { return applyToJSON(item, false) })}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getApply(w http.ResponseWriter, r *http.Request) {
	found, err := s.manifest.GetApply(r.Context(), r.PathValue("id"))
	if errors.Is(err, manifest.ErrApplyNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, applyToJSON(found, true))
}

// RefusalKey traduit en clé du catalogue le refus qu'un composant du domaine
// oppose à un geste du fichier ; vide si l'erreur n'est pas un refus. C'est
// ce que manifest.SetErrorKeys reçoit : le paquet manifest ne connaît pas
// les clés du serveur.
func RefusalKey(err error) string {
	return manifestProblemKey(err)
}

// manifestProblemKey essaie les refus de chaque domaine que le fichier écrit.
func manifestProblemKey(err error) string {
	if errors.Is(err, directory.ErrServiceLinked) {
		return "directory.service_linked"
	}
	for _, refusal := range []func(error) (string, bool){directoryRefusalKey, projectRefusalKey, probeRefusalKey, jobRefusalKey} {
		if key, refused := refusal(err); refused {
			return key
		}
	}
	return ""
}
