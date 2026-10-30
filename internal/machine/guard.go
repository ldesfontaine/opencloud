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

// UnitFile est l'unité de l'agent, telle que la migration la modifie.
const UnitFile = "/etc/systemd/system/" + brand.AgentUnit

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

// GuardMigrationLines sont les lignes qu'un opérateur non root, avec sudo,
// colle sur une machine dont l'agent est déjà installé : arrêter l'agent, poser
// le lanceur et la nouvelle version de l'agent (le binaire de {source}, la
// version {number} que ce serveur est), faire lancer le lanceur par l'unité,
// puis relancer. Rejouées, elles ne changent rien de plus (le sed ne trouve
// plus l'ancienne ligne).
func GuardMigrationLines(source, number string) []string {
	asAccount := "sudo -u " + brand.AgentAccount + " "
	binDir := brand.AgentStateDir + "/" + brand.AgentBinDir
	binaryName := brand.AgentBinaryPrefix + number
	oldExecStart := "ExecStart=" + brand.AgentCurrentPath + " "
	lines := []string{
		"sudo systemctl stop " + brand.AgentUnit,
	}
	for _, line := range GuardInstallLines(source) {
		lines = append(lines, "sudo "+line)
	}
	lines = append(lines,
		asAccount+"install -m 0755 "+source+" "+binDir+"/"+binaryName,
		asAccount+"ln -sfn "+binaryName+" "+binDir+"/"+brand.AgentCurrentLink,
		"sudo sed -i 's|^"+oldExecStart+"|ExecStart="+guardExecStart+"|' "+UnitFile,
		"systemctl cat "+brand.AgentUnit+" | grep '^ExecStart='",
		"sudo systemctl daemon-reload",
		"sudo systemctl start "+brand.AgentUnit,
	)
	return lines
}

// GuardMigrationText est GuardMigrationLines en un bloc à copier.
func GuardMigrationText(source, number string) string {
	return strings.Join(GuardMigrationLines(source, number), "\n")
}
