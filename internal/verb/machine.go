package verb

// Le geste qui retire une machine ne se défait pas : il est offert à un jeton
// comme verbe à confirmation. Le jeton dépose une demande, l'opérateur la
// confirme dans l'interface, et seulement alors la route d'opérateur s'exécute.
func init() {
	Default.MustRegister(Verb{
		ID: "machine.delete", Route: "DELETE /api/machines/{id}", Params: []string{"id"}, Body: []string{}, Target: TargetMachine,
		Label: "api_token.verb_machine_delete", Summary: "api_token.verb_machine_delete_text", Confirm: true,
	})
}
