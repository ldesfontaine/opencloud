package machine

import (
	"github.com/ldesfontaine/opencloud/internal/brand"
)

// Les droits proxmox.power et proxmox.snapshot ne se proposent que sur une
// machine où l'agent a reconnu un hôte Proxmox VE (rôle proxmox_ve). Ils
// s'appuient sur un jeton Proxmox local, posé par l'opérateur dans l'état de
// l'agent : ce que l'opérateur crée dans Proxmox, et ce qu'il copie sur
// l'hôte, s'écrit ici une seule fois. Le serveur n'en voit jamais le secret.

// ProxmoxLocalAPI est l'API que l'agent appelle : celle de son propre hôte,
// sur la boucle locale.
const ProxmoxLocalAPI = "https://127.0.0.1:8006"

// ProxmoxTokenFile est le fichier du jeton, dans l'état de l'agent.
const ProxmoxTokenFile = brand.AgentStateDir + "/" + brand.AgentProxmoxFile

// ProxmoxTokenID est l'identifiant complet du jeton, tel que Proxmox le nomme.
const ProxmoxTokenID = brand.ProxmoxUser + "!" + brand.ProxmoxTokenName

// ProxmoxRolePrivileges sont les privilèges du rôle réduit. VM.Audit ne sert
// qu'à lire l'état d'un invité avant d'agir (Proxmox exige ce privilège pour
// le lister) : il n'ajoute aucun pouvoir d'écriture.
const ProxmoxRolePrivileges = "VM.Audit VM.PowerMgmt VM.Snapshot"

// ProxmoxTokenCommands sont les commandes que l'opérateur joue en root sur
// l'hôte Proxmox, dans l'ordre. Aucune n'est jouée par openCloud. Les quatre
// premières créent le rôle, le compte et le jeton ; les trois dernières posent
// le secret et l'empreinte dans le fichier de l'agent : le secret se saisit
// sans écho, il n'entre dans aucun argument de commande ni dans l'historique du
// shell. Aucune apostrophe dans les textes, aucune accolade.
func ProxmoxTokenCommands() []string {
	file := ProxmoxTokenFile
	return []string{
		"pveum role add " + brand.ProxmoxRole + ` --privs "` + ProxmoxRolePrivileges + `"`,
		"pveum user add " + brand.ProxmoxUser,
		"pveum acl modify / --users " + brand.ProxmoxUser + " --roles " + brand.ProxmoxRole,
		"pveum user token add " + brand.ProxmoxUser + " " + brand.ProxmoxTokenName + " --privsep 0",
		"install -m 0600 -o " + brand.AgentAccount + " -g " + brand.AgentAccount + " /dev/null " + file,
		`read -rsp "Secret : " PVE_SECRET; echo`,
		`printf '{"token_id":"%s","secret":"%s","fingerprint":"%s"}\n' '` + ProxmoxTokenID + `' "$PVE_SECRET" ` +
			`"$(openssl s_client -connect 127.0.0.1:8006 </dev/null 2>/dev/null | openssl x509 -noout -fingerprint -sha256 | cut -d= -f2)" > ` +
			file + "; unset PVE_SECRET",
	}
}

// ProxmoxRights dit si la liste porte l'un des deux droits Proxmox : la page
// du jeton montre alors les commandes.
func ProxmoxRights(capabilities []Capability) bool {
	return HasCapability(capabilities, CapabilityProxmoxPower) || HasCapability(capabilities, CapabilityProxmoxSnapshot)
}
