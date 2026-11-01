package machine

import (
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// Le lanceur stable de l'agent : une copie du binaire que root pose une fois
// hors de bin/, et que l'agent ne remplace jamais (un agent qui s'est cassé ne
// peut pas se réparer lui-même). L'unité lance cette copie, qui lance
// bin/current et rebascule sur la version d'avant si une mise à jour n'est pas
// confirmée. Les lignes ci-dessous sont celles de l'installation et de la
// migration d'un agent déjà installé ; aucune ne porte de valeur de
// l'opérateur : des constantes et le numéro de version du serveur.

// guardExecStart est ce que l'unité lance quand le lanceur est en place : la
// copie de root, sa commande guard, puis la ligne de l'agent (« agent -state
// … ») que l'unité garde telle quelle.
const guardExecStart = brand.AgentGuardPath + " guard -state " + brand.AgentStateDir + " "

// GuardInstallLines pose le lanceur à l'installation, en root : le dossier de
// root et la copie du binaire, source étant le binaire à copier.
func GuardInstallLines(source string) []string {
	return []string{
		"install -d -m 0755 -o root -g root " + brand.AgentGuardDir,
		"install -m 0755 -o root -g root " + source + " " + brand.AgentGuardPath,
	}
}

// ReleaseDownloadLines télécharge le binaire de la version {number} publiée,
// vérifie sa somme SHA-256 contre SHA256SUMS et le pose dans /tmp/opencloud :
// là où la préparation et la migration le prennent. Un bloc, joué en root ; il
// s'arrête au premier échec (réseau, 404, somme fausse) sans rien poser.
// L'architecture est celle de la machine (uname -m). La signature d'une version
// signée se vérifie à part (installation.md) : ici, la somme.
func ReleaseDownloadLines(number string) []string {
	binary := brand.ReleaseBinaryPrefix + "$arch"
	return []string{
		"(cd \"$(mktemp -d)\" &&",
		"base=" + brand.ReleaseBaseURL + "/" + number + " &&",
		`arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') &&`,
		`curl -fLO "$base/` + binary + `" -fLO "$base/` + brand.ReleaseSumsFile + `" &&`,
		"sha256sum -c --ignore-missing " + brand.ReleaseSumsFile + " &&",
		`install -m 0755 "` + binary + `" /tmp/` + brand.Command + ")",
	}
}

// GuardMigrationLines sont les lignes qu'un opérateur colle sur une machine dont
// l'agent est déjà installé : arrêter l'agent, poser le lanceur stable et la
// nouvelle version de l'agent (le binaire de {source}, la version {number} que
// ce serveur est), faire lancer le lanceur par l'unité, puis relancer. Rejouées,
// elles ne changent rien de plus (le sed ne trouve plus l'ancienne ligne).
// root : l'opérateur est root (su -), sans sudo, comme sur un hôte Proxmox ;
// le compte de l'agent se prend alors par runuser. Sinon, sudo.
func GuardMigrationLines(source, number string, root bool) []string {
	privileged, asAccount := "sudo ", "sudo -u "+brand.AgentAccount+" "
	if root {
		privileged, asAccount = "", "runuser -u "+brand.AgentAccount+" -- "
	}
	binDir := brand.AgentStateDir + "/" + brand.AgentBinDir
	binaryName := brand.AgentBinaryPrefix + number
	oldExecStart := "ExecStart=" + brand.AgentCurrentPath + " "
	lines := []string{
		privileged + "systemctl stop " + brand.AgentUnit,
	}
	for _, line := range GuardInstallLines(source) {
		lines = append(lines, privileged+line)
	}
	lines = append(lines,
		asAccount+"install -m 0755 "+source+" "+binDir+"/"+binaryName,
		asAccount+"ln -sfn "+binaryName+" "+binDir+"/"+brand.AgentCurrentLink,
		privileged+"sed -i 's|^"+oldExecStart+"|ExecStart="+guardExecStart+"|' "+UnitFile,
		"systemctl cat "+brand.AgentUnit+" | grep '^ExecStart='",
		privileged+"systemctl daemon-reload",
		privileged+"systemctl start "+brand.AgentUnit,
	)
	return lines
}

// GuardMigrationText est la migration en un bloc à copier : le téléchargement
// vérifié du binaire, puis GuardMigrationLines. Rien n'est supposé déjà copié.
func GuardMigrationText(number string, root bool) string {
	source := "/tmp/" + brand.Command
	lines := append(ReleaseDownloadLines(number), GuardMigrationLines(source, number, root)...)
	return strings.Join(lines, "\n")
}
