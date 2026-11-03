package machine

import (
	"path"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// UnitFile est l'unité de l'agent, telle que la migration la modifie.
const UnitFile = "/etc/systemd/system/" + brand.AgentUnit

// RightsDropInFile est le drop-in qui autorise les dossiers où les droits de
// l'agent écrivent : l'unité n'écrit que dans son état (ProtectSystem=strict).
const RightsDropInFile = ProduceDropInDir + "/droits.conf"

// AgentUnitLines est l'unité de l'agent que la page d'installation donne à
// poser, sans les commentaires de docs/installation/opencloud-agent.service :
// ce fichier reste le texte de référence, et un test échoue si les deux
// diffèrent.
func AgentUnitLines() []string {
	state := brand.AgentStateDir
	return []string{
		"[Unit]",
		"Description=" + brand.ProductName + " (agent)",
		"Documentation=" + brand.InstallDocURL,
		"After=network-online.target docker.service",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"User=" + brand.AgentAccount,
		"Group=" + brand.AgentAccount,
		"ExecStart=" + brand.AgentGuardPath + " guard -state " + state + " agent -state " + state,
		"StateDirectory=" + path.Base(state),
		"StateDirectoryMode=0700",
		"Restart=on-failure",
		"RestartSec=10",
		"RestartPreventExitStatus=2",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ProtectHome=read-only",
		"PrivateTmp=true",
		"UMask=0077",
		"ProtectKernelTunables=true",
		"ProtectKernelModules=true",
		"ProtectKernelLogs=true",
		"ProtectControlGroups=true",
		"ProtectClock=true",
		"RestrictNamespaces=true",
		"RestrictRealtime=true",
		"RestrictSUIDSGID=true",
		"LockPersonality=true",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
	}
}
