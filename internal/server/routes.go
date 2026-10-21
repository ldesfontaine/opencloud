package server

import (
	"net/http"
	"strings"
)

// La méthode « * » : toutes, pour un fourre-tout.
const anyMethod = "*"

type route struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// L'arbre des routes, figé par routes_test.go : tout ajout casse le test,
// c'est voulu. Sous /api, ce que le front appelle, en JSON : une route par
// lecture, une route par action. Sous /agent, ce que l'agent d'une machine
// appelle (paquet agentapi) ; sous /ping, ce qu'un cron appelle, sans
// authentification : le jeton est le secret (paquet ping). Sous /statut, la
// page publique et ses routes, sans authentification et limitées en débit
// (paquet public). Ces trois paquets déclarent leurs routes, montées ici à
// leur place. Sous /oauth et /mcp, et les deux métadonnées well-known, ce qu'un
// agent IA appelle, par Bearer, fermé tant que MCP n'est pas activé. Tout
// le reste est le front, servi par spa.go. Ce qui revient à l'opérateur
// passe par son authentification, ou attend la première configuration tant
// qu'aucun compte n'existe : voir auth_middleware.go.
func (s *Server) routes() []route {
	routes := []route{
		{"GET", "/api/session", s.session},
		{"PUT", "/api/session/language", s.setLanguage},
		{"GET", "/api/i18n/{code}", s.catalogAPI},
		{"GET", "/api/counts", s.counts},
		{"GET", "/api/events", s.events},
		{"GET", "/api/machines", s.listMachines},
		{"POST", "/api/machines/tokens", s.grantsAuthority(s.createMachineToken)},
		{"DELETE", "/api/machines/tokens/{id}", s.cancelMachineToken},
		{"GET", "/api/machines/{id}", s.getMachine},
		{"DELETE", "/api/machines/{id}", s.removeMachine},
		{"GET", "/api/machines/{id}/delete-plan", s.getMachineDeletePlan},
		{"POST", "/api/machines/{id}/actions/reenroll", s.grantsAuthority(s.reenrollMachine)},
		{"POST", "/api/machines/{id}/actions/check-updates", s.checkUpdates},
		{"GET", "/api/machines/{id}/resources", s.getMachineResources},
		{"GET", "/api/machines/{id}/resources/history", s.getMachineHistory},
		{"GET", "/api/resources", s.listResources},
		{"GET", "/api/machines/{id}/containers", s.listMachineContainers},
		{"GET", "/api/machines/{id}/network", s.getMachineNetwork},
		{"GET", "/api/machines/{id}/probes", s.listMachineProbes},
		{"GET", "/api/network", s.listNetworks},
		{"GET", "/api/containers", s.listContainers},
		{"GET", "/api/containers/{id}", s.getContainer},
		{"GET", "/api/containers/{id}/transitions", s.listContainerTransitions},
		{"GET", "/api/containers/{id}/logs", s.getContainerLogs},
		{"GET", "/api/containers/{id}/logs/stream", s.streamContainerLogs},
		{"GET", "/api/directory/services", s.listDeclaredServices},
		{"POST", "/api/directory/services", s.createDeclaredService},
		{"GET", "/api/directory/services/{id}", s.getDeclaredService},
		{"PUT", "/api/directory/services/{id}", s.updateDeclaredService},
		{"DELETE", "/api/directory/services/{id}", s.deleteDeclaredService},
		{"GET", "/api/directory/services/{id}/delete-plan", s.getDeclaredServiceDeletePlan},
		{"POST", "/api/directory/services/{id}/actions/review", s.reviewDeclaredService},
		{"PUT", "/api/directory/units/{id}/update-policy", s.setUnitUpdatePolicy},
		{"GET", "/api/directory/projects", s.listProjects},
		{"POST", "/api/directory/projects", s.createProject},
		{"GET", "/api/directory/projects/{id}", s.getProject},
		{"PUT", "/api/directory/projects/{id}", s.updateProject},
		{"DELETE", "/api/directory/projects/{id}", s.deleteProject},
		{"GET", "/api/directory/projects/{id}/delete-plan", s.getProjectDeletePlan},
		{"GET", "/api/directory/gaps", s.getDirectoryGaps},
		{"GET", "/api/directory/proposal", s.getDirectoryProposal},
		{"POST", "/api/directory/ignored", s.ignoreKey},
		{"DELETE", "/api/directory/ignored", s.unignoreKey},
		{"GET", "/api/manifest/export", s.exportManifest},
		{"POST", "/api/manifest/plan", s.planManifest},
		{"POST", "/api/manifest/apply", s.applyManifest},
		{"GET", "/api/manifest/applies", s.listApplies},
		{"GET", "/api/manifest/applies/{id}", s.getApply},
		{"GET", "/api/probes", s.listProbes},
		{"POST", "/api/probes", s.createProbe},
		{"GET", "/api/probes/{id}", s.getProbe},
		{"PUT", "/api/probes/{id}", s.updateProbe},
		{"DELETE", "/api/probes/{id}", s.deleteProbe},
		{"GET", "/api/probes/{id}/delete-plan", s.getProbeDeletePlan},
		{"POST", "/api/probes/{id}/actions/pause", s.pauseProbe},
		{"POST", "/api/probes/{id}/actions/resume", s.resumeProbe},
		{"GET", "/api/jobs", s.listJobs},
		{"POST", "/api/jobs", s.createJob},
		{"GET", "/api/jobs/{id}", s.getJob},
		{"PUT", "/api/jobs/{id}", s.updateJob},
		{"DELETE", "/api/jobs/{id}", s.deleteJob},
		{"GET", "/api/jobs/{id}/delete-plan", s.getJobDeletePlan},
		{"POST", "/api/jobs/{id}/actions/pause", s.pauseJob},
		{"POST", "/api/jobs/{id}/actions/resume", s.resumeJob},
		{"POST", "/api/jobs/{id}/actions/regenerate-token", s.regenerateJobToken},
		{"GET", "/api/status/components", s.listComponents},
		{"POST", "/api/status/components", s.createComponent},
		{"GET", "/api/status/components/{id}", s.getComponent},
		{"PUT", "/api/status/components/{id}", s.updateComponent},
		{"DELETE", "/api/status/components/{id}", s.deleteComponent},
		{"GET", "/api/status/components/{id}/delete-plan", s.getComponentDeletePlan},
		{"GET", "/api/status/incidents", s.listIncidents},
		{"POST", "/api/status/incidents", s.openIncident},
		{"GET", "/api/status/incidents/{id}", s.getIncident},
		{"PUT", "/api/status/incidents/{id}", s.changeIncident},
		{"DELETE", "/api/status/incidents/{id}", s.deleteIncident},
		{"POST", "/api/status/incidents/{id}/updates", s.addIncidentUpdate},
		{"GET", "/api/status/page", s.getStatusPage},
		{"PUT", "/api/status/page", s.setStatusPage},
		{"GET", "/api/alerts", s.listAlerts},
		{"GET", "/api/alerts/channels", s.listChannels},
		{"POST", "/api/alerts/channels", s.createChannel},
		{"GET", "/api/alerts/channels/{id}", s.getChannel},
		{"PUT", "/api/alerts/channels/{id}", s.updateChannel},
		{"DELETE", "/api/alerts/channels/{id}", s.deleteChannel},
		{"POST", "/api/alerts/channels/{id}/actions/test", s.testChannel},
		{"GET", "/api/alerts/silences", s.listSilences},
		{"POST", "/api/alerts/silences", s.createSilence},
		{"DELETE", "/api/alerts/silences/{id}", s.deleteSilence},
		{"POST", "/api/alerts/actions/acknowledge", s.acknowledgeAlerts},
		{"GET", "/api/alerts/{id}", s.getAlert},
		{"POST", "/api/alerts/{id}/actions/acknowledge", s.acknowledgeAlert},
		{"GET", "/api/mcp", s.getMCP},
		{"POST", "/api/mcp/actions/enable", s.grantsAuthority(s.enableMCP)},
		{"POST", "/api/mcp/actions/disable", s.disableMCP},
		{"POST", "/api/mcp/actions/regenerate-secret", s.grantsAuthority(s.regenerateMCPSecret)},
		{"PUT", "/api/mcp/redirect-uris", s.grantsAuthority(s.setMCPRedirectURIs)},
		{"POST", "/api/mcp/tokens", s.grantsAuthority(s.createMCPToken)},
		{"DELETE", "/api/mcp/tokens/{id}", s.revokeMCPToken},
		{"DELETE", "/api/mcp/sessions/{id}", s.revokeMCPSession},
		{"GET", "/api/settings/disk", s.getDiskSettings},
		{"PUT", "/api/settings/disk", s.setDiskSettings},
		{"GET", "/api/settings/lifecycle", s.getLifecycleSettings},
		{"PUT", "/api/settings/lifecycle", s.setLifecycleSettings},
		{"GET", "/api/settings/mail", s.getMailSettings},
		{"PUT", "/api/settings/mail", s.setMailSettings},
		{"POST", "/api/settings/mail/actions/test", s.testMailSettings},
		{"GET", "/api/auth", s.getAuth},
		{"POST", "/api/auth/actions/enable", s.enableAuth},
		{"POST", "/api/auth/actions/disable", s.disableAuth},
		{"PUT", "/api/auth/password", s.changeAuthPassword},
		{anyMethod, "/api/", s.apiUnknown},
	}
	add := func(method, pattern string, handler http.HandlerFunc) {
		routes = append(routes, route{method, pattern, handler})
	}
	s.agent.Routes(add)
	s.pings.Routes(add)
	s.public.Routes(add)
	return append(routes, []route{
		{"GET", "/.well-known/oauth-authorization-server", s.oauthServerMetadata},
		{"GET", "/.well-known/oauth-protected-resource", s.oauthResourceMetadata},
		{"GET", "/.well-known/oauth-protected-resource/mcp", s.oauthResourceMetadata},
		{"GET", mcpAuthorizePath, s.grantsAuthority(s.oauthAuthorize)},
		{"POST", "/oauth/token", s.oauthToken},
		{anyMethod, "/mcp", s.serveMCP},
		{anyMethod, "/", s.serveApp},
	}...)
}

// Toute écriture sous /api passe par la garde même-origine ; les lectures
// et les routes de l'agent, des pings et de la page publique n'en ont pas
// besoin. L'accès de l'opérateur enveloppe le tout, avant la garde. Ce qu'un navigateur appelle
// avec l'état d'openCloud, /api et l'autorisation OAuth, exige en plus un
// Host reconnu, jugé avant tout le reste, authentification comprise.
func (s *Server) mux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, route := range s.routes() {
		handler := route.handler
		isAPI := strings.HasPrefix(route.pattern, "/api/")
		if isAPI && route.method != http.MethodGet {
			handler = s.requireSameOrigin(handler)
		}
		if isOperatorRoute(route.pattern) {
			handler = s.requireOperator(handler, isFirstSetupRoute(route.method, route.pattern))
		}
		if isAPI || route.pattern == mcpAuthorizePath {
			handler = s.requireKnownHost(handler)
		}
		pattern := route.pattern
		if route.method != anyMethod {
			pattern = route.method + " " + pattern
		}
		mux.HandleFunc(pattern, handler)
	}
	return mux
}

func (s *Server) apiUnknown(w http.ResponseWriter, _ *http.Request) {
	s.apiNotFound(w)
}
