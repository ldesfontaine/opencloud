package web

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/runner"
	"github.com/ldesfontaine/opencloud/internal/store"
	assets "github.com/ldesfontaine/opencloud/web"
)

// Authenticator est ce que web attend de auth : connexion, session, mot de
// passe. Le vrai est *auth.Service ; l'interface vit ici, côté consommateur,
// et ne dit que ce que web utilise.
type Authenticator interface {
	Login(ctx context.Context, username, password string) (auth.Session, error)
	Logout(ctx context.Context, token string) error
	Authenticate(ctx context.Context, token string) (store.Account, error)
	ChangePassword(ctx context.Context, accountID int64, currentPassword, newPassword string) (auth.Session, error)
	PasswordPolicy() auth.PasswordPolicy
}

// Machines est la lecture des machines. Le vrai est *store.Store.
type Machines interface {
	Machines(ctx context.Context) ([]store.Machine, error)
	Machine(ctx context.Context, id string) (store.Machine, error)
}

// MachineDeclaration est l'écriture : déclarer une machine, changer son
// adresse et son port. Séparée de la lecture, parce que les pages d'action ne
// demandent que la lecture.
type MachineDeclaration interface {
	Insert(ctx context.Context, machine store.Machine) error
	UpdateAccess(ctx context.Context, id string, address string, port int) error
}

// EnrolmentStatus dit si la machine a un accès posé, et depuis quand.
type EnrolmentStatus struct {
	Enrolled bool
	Since    time.Time
}

// Enrolment est ce que web lit de enroll. Tant que la dépendance est nulle,
// aucune machine n'est enrôlée et l'interface le dit.
type Enrolment interface {
	Status(ctx context.Context, machineID string) (EnrolmentStatus, error)
}

// Enroller est l'enrôlement en deux temps : Prepare engendre la clé et rend
// la commande à coller sur la machine, Confirm relève la clé d'hôte et refuse
// si l'empreinte saisie ne correspond pas. Sans elle, l'interface montre
// l'état d'enrôlement et n'enrôle rien.
type Enroller interface {
	// Prepare est rejouable : la clé n'est engendrée qu'une fois, la fiche
	// réaffiche la même commande tant que l'empreinte n'est pas confirmée.
	Prepare(ctx context.Context, machine store.Machine) (string, error)
	Confirm(ctx context.Context, machine store.Machine, fingerprint string) error
	UpdateAccess(ctx context.Context, machine store.Machine, fingerprint string) error
}

// MachineHealth est ce que la dernière exécution de « Tester l'accès » a
// constaté. ProbeState vaut "", "reachable", "ssh-failed" ou
// "launcher-failed" ; ProbedAt nul veut dire jamais sondée.
type MachineHealth struct {
	ProbedAt   time.Time
	ProbeState string
	ProbeNote  string
}

// Prober est ce que web attend de probe : lancer « Tester l'accès » tout de
// suite, et relire ce que la dernière exécution a constaté.
type Prober interface {
	Now(ctx context.Context, machineID string) error
	Health(ctx context.Context, machineID string) (MachineHealth, error)
}

// Actions est ce que web attend du runner : déposer, suivre en direct, et
// relire le journal. web ne lance rien lui-même.
type Actions interface {
	Enqueue(ctx context.Context, machineID string, kind catalog.Kind, params map[string]string) (store.Action, error)
	Subscribe(actionID string) (<-chan runner.Event, func())
	Action(ctx context.Context, id string) (store.Action, error)
	ActionsForMachine(ctx context.Context, machineID string, limit int) ([]store.Action, error)
	Lines(ctx context.Context, actionID string, afterSeq int64) ([]store.ActionLine, error)
}

// Catalog est ce que web attend du catalogue pour l'écran « avant ».
type Catalog interface {
	Definitions() []catalog.Definition
	Lookup(kind catalog.Kind) (catalog.Definition, bool)
	Prepare(kind catalog.Kind, params map[string]string) (catalog.Prepared, error)
}

// Dependencies : tout ce que l'interface consomme. Seul Auth est exigé ; sans
// les autres, l'interface se comporte comme avant les actions.
type Dependencies struct {
	Auth        Authenticator
	Machines    Machines
	Declaration MachineDeclaration
	Enrolment   Enrolment
	Enroller    Enroller
	Actions     Actions
	Catalog     Catalog
	Prober      Prober
}

type Server struct {
	auth        Authenticator
	machines    Machines
	declaration MachineDeclaration
	enrolment   Enrolment
	enroller    Enroller
	actions     Actions
	catalog     Catalog
	prober      Prober

	logger    *slog.Logger
	version   string
	templates pageTemplates
	static    http.Handler
	csrf      csrfSigner
}

func New(deps Dependencies, version string, logger *slog.Logger) (*Server, error) {
	templates, err := parsePageTemplates(assets.Templates)
	if err != nil {
		return nil, err
	}

	staticFiles, err := fs.Sub(assets.Static, "static")
	if err != nil {
		return nil, fmt.Errorf("locate static files: %w", err)
	}

	csrf, err := newCSRFSigner()
	if err != nil {
		return nil, err
	}

	return &Server{
		auth:        deps.Auth,
		machines:    deps.Machines,
		declaration: deps.Declaration,
		enrolment:   deps.Enrolment,
		enroller:    deps.Enroller,
		actions:     deps.Actions,
		catalog:     deps.Catalog,
		prober:      deps.Prober,
		logger:      logger,
		version:     version,
		templates:   templates,
		static:      http.StripPrefix("/static/", http.FileServerFS(staticFiles)),
		csrf:        csrf,
	}, nil
}

// Une route : méthode et motif au format de http.ServeMux, et son handler.
type route struct {
	pattern string
	handler http.Handler
}

// La table des routes, la seule. Routes la liste, Handler la sert.
func (s *Server) routes() []route {
	return []route{
		{"GET /{$}", s.requireAccount(s.showInfrastructure)},
		{"GET /actions/{id}", s.requireAccount(s.showAction)},
		{"GET /actions/{id}/stream", s.requireAccount(s.streamAction)},
		{"GET /healthz", http.HandlerFunc(s.showHealth)},
		{"GET /login", http.HandlerFunc(s.showLogin)},
		{"POST /login", http.HandlerFunc(s.submitLogin)},
		{"POST /logout", http.HandlerFunc(s.submitLogout)},
		{"POST /machines", s.requireAccount(s.submitMachine)},
		{"GET /machines/new", s.requireAccount(s.showMachineForm)},
		{"GET /machines/{id}", s.requireAccount(s.showMachine)},
		{"POST /machines/{id}/access", s.requireAccount(s.submitAccess)},
		{"GET /machines/{id}/actions/{kind}", s.requireAccount(s.showActionForm)},
		{"POST /machines/{id}/actions/{kind}", s.requireAccount(s.submitAction)},
		{"POST /machines/{id}/confirm", s.requireAccount(s.submitFingerprint)},
		{"POST /machines/{id}/probe", s.requireAccount(s.submitProbe)},
		{"GET /password", s.requireAccount(s.showPasswordChange)},
		{"POST /password", s.requireAccount(s.submitPasswordChange)},
		{"GET /static/", s.static},
	}
}

// Routes liste les motifs servis. Figée par un test : toute route ajoutée ou
// retirée se voit.
func (s *Server) Routes() []string {
	var patterns []string
	for _, current := range s.routes() {
		patterns = append(patterns, current.pattern)
	}
	return patterns
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, current := range s.routes() {
		mux.Handle(current.pattern, current.handler)
	}
	// Seconde ligne de défense anti-CSRF, par les en-têtes Sec-Fetch-Site et
	// Origin du navigateur (OWASP : « Fetch Metadata »). Le jeton signé reste
	// la première.
	crossOrigin := http.NewCrossOriginProtection()
	return secureHeaders(limitRequestBody(crossOrigin.Handler(mux)))
}

// Les pages des machines et des actions n'existent que lorsque le runner et le
// catalogue sont branchés ; sans eux, l'interface est celle du squelette.
func (s *Server) actionsReady() bool {
	return s.machines != nil && s.actions != nil && s.catalog != nil
}

// Déclarer une machine demande l'écriture du store et l'enrôlement : sans
// l'un des deux, les pages d'enrôlement n'existent pas.
func (s *Server) enrolmentReady() bool {
	return s.machines != nil && s.declaration != nil && s.enroller != nil
}

// secureHeaders : aucun script ni style externe, jamais dans un cadre, pas de
// cache des pages — elles sont toutes privées.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; form-action 'self'")
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		headers.Set("Referrer-Policy", "same-origin")
		headers.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
