package verb

// Les gestes de mise à jour de l'agent d'une machine : mettre à jour et revenir
// en arrière se défont l'un l'autre. Ni l'un ni l'autre ne signe ni n'importe
// de version : le serveur n'envoie que sa propre version, déjà importée et
// signée par l'opérateur (`release import` n'est pas un verbe et ne le sera
// pas). Le serveur refuse tout agent qui ne sait pas revenir seul à sa version
// d'avant, et ne met jamais deux agents à jour en même temps.
func init() {
	Default.MustRegister(Verb{
		ID: "agent.update", Route: "POST /api/machines/{id}/actions/update-agent", Params: []string{"id"}, Body: []string{}, Target: TargetMachine,
		Label: "api_token.verb_agent_update", Summary: "api_token.verb_agent_update_text", Reverse: "agent.rollback",
	})
	Default.MustRegister(Verb{
		ID: "agent.rollback", Route: "POST /api/machines/{id}/actions/rollback-agent", Params: []string{"id"}, Body: []string{}, Target: TargetMachine,
		Label: "api_token.verb_agent_rollback", Summary: "api_token.verb_agent_rollback_text", Reverse: "agent.update",
	})
}
