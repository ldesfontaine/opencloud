package verb

// Les gestes de l'agent de l'hôte Proxmox. start et shutdown se défont l'un
// l'autre ; snapshot et snapshot_delete aussi. Aucun ne force : l'arrêt est
// toujours propre, et la suppression ne touche que les instantanés créés par
// ce produit (le serveur et l'agent le revérifient, le jeton n'y change rien).
func init() {
	Default.MustRegister(Verb{
		ID: "proxmox.vm.start", Route: "POST /api/machines/{id}/proxmox/guests/{vmid}/actions/start",
		Params: []string{"id", "vmid"}, Body: []string{},
		Label: "api_token.verb_proxmox_vm_start", Summary: "api_token.verb_proxmox_vm_start_text", Reverse: "proxmox.vm.shutdown",
	})
	Default.MustRegister(Verb{
		ID: "proxmox.vm.shutdown", Route: "POST /api/machines/{id}/proxmox/guests/{vmid}/actions/shutdown",
		Params: []string{"id", "vmid"}, Body: []string{},
		Label: "api_token.verb_proxmox_vm_shutdown", Summary: "api_token.verb_proxmox_vm_shutdown_text", Reverse: "proxmox.vm.start",
	})
	Default.MustRegister(Verb{
		ID: "proxmox.vm.snapshot", Route: "POST /api/machines/{id}/proxmox/guests/{vmid}/actions/snapshot",
		Params: []string{"id", "vmid"}, Body: []string{},
		Label: "api_token.verb_proxmox_vm_snapshot", Summary: "api_token.verb_proxmox_vm_snapshot_text", Reverse: "proxmox.vm.snapshot_delete",
	})
	Default.MustRegister(Verb{
		ID: "proxmox.vm.snapshot_delete", Route: "POST /api/machines/{id}/proxmox/guests/{vmid}/actions/snapshot-delete",
		Params: []string{"id", "vmid"}, Body: []string{"snapshot"},
		Label: "api_token.verb_proxmox_vm_snapshot_delete", Summary: "api_token.verb_proxmox_vm_snapshot_delete_text", Reverse: "proxmox.vm.snapshot",
	})
}
