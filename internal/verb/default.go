package verb

// Default est la liste que le serveur et la création d'un jeton partagent.
// Un chantier qui ouvre de nouveaux gestes (l'hyperviseur, par exemple)
// ajoute un fichier de ce paquet qui appelle Default.MustRegister dans un
// init : l'admission n'a pas à changer.
var Default = newDefault()

// Les verbes de départ sont ceux dont l'inverse existe côté opérateur.
// alert.acknowledge n'en a pas (rien ne désacquitte une alerte) : il reste
// réservé à l'opérateur.
func newDefault() *Registry {
	registry := &Registry{}
	registry.MustRegister(Verb{
		ID: "agent.ping", Route: "POST /api/machines/{id}/actions/ping", Params: []string{"id"}, Body: []string{},
		Label: "api_token.verb_agent_ping", Summary: "api_token.verb_agent_ping_text", Reverse: "agent.ping",
	})
	registry.MustRegister(Verb{
		ID: "probe.pause", Route: "POST /api/probes/{id}/actions/pause", Params: []string{"id"}, Body: []string{},
		Label: "api_token.verb_probe_pause", Summary: "api_token.verb_probe_pause_text", Reverse: "probe.resume",
	})
	registry.MustRegister(Verb{
		ID: "probe.resume", Route: "POST /api/probes/{id}/actions/resume", Params: []string{"id"}, Body: []string{},
		Label: "api_token.verb_probe_resume", Summary: "api_token.verb_probe_resume_text", Reverse: "probe.pause",
	})
	registry.MustRegister(Verb{
		ID: "job.pause", Route: "POST /api/jobs/{id}/actions/pause", Params: []string{"id"}, Body: []string{},
		Label: "api_token.verb_job_pause", Summary: "api_token.verb_job_pause_text", Reverse: "job.resume",
	})
	registry.MustRegister(Verb{
		ID: "job.resume", Route: "POST /api/jobs/{id}/actions/resume", Params: []string{"id"}, Body: []string{},
		Label: "api_token.verb_job_resume", Summary: "api_token.verb_job_resume_text", Reverse: "job.pause",
	})
	return registry
}
