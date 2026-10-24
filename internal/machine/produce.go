package machine

import (
	"regexp"
	"slices"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Le droit backup.produce : l'agent sait produire une sauvegarde avec ce
// producteur. Sa portée n'est pas un chemin mais un ou plusieurs noms de
// producteurs, séparés par « : » comme les chemins des autres droits
// (backup.produce=proxmox-vzdump) : le champ Capability.Paths les porte.

// ProducerProxmoxVzdump sauvegarde une VM ou un conteneur Proxmox par vzdump.
const ProducerProxmoxVzdump = "proxmox-vzdump"

// Producers rend la liste fermée des producteurs qu'un agent peut recevoir.
func Producers() []string {
	return []string{ProducerProxmoxVzdump}
}

const maxCapabilityProducers = 4

var producerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

var ErrCapabilityProducer = refusal.Invalid("machine.capability_producer", "backup.produce takes producer names from the closed list, in lowercase letters, digits and dashes")

// normalizeProducers juge la portée de backup.produce : un à quatre
// producteurs connus, triés, sans doublon.
func normalizeProducers(capability Capability) ([]string, error) {
	producers := slices.Clone(capability.Paths)
	slices.Sort(producers)
	producers = slices.Compact(producers)
	if len(producers) == 0 || len(producers) > maxCapabilityProducers {
		return nil, ErrCapabilityScope
	}
	for _, producer := range producers {
		if !producerPattern.MatchString(producer) || !slices.Contains(Producers(), producer) {
			return nil, ErrCapabilityProducer
		}
	}
	return producers, nil
}

// CoversProducer dit si le droit backup.produce de la liste nomme ce
// producteur.
func CoversProducer(capabilities []Capability, name string) bool {
	return slices.ContainsFunc(capabilities, func(capability Capability) bool {
		return capability.Name == CapabilityBackupProduce && slices.Contains(capability.Paths, name)
	})
}

// Ce que la préparation d'une machine pose pour backup.produce, et que
// l'agent vérifie avant de s'en servir. Les textes vivent ici, une seule fois :
// la page du jeton les écrit, un test les relit, la documentation les cite.
// vzdump tourne sous systemd-run, donc hors du bac à sable de l'unité de
// l'agent (ProtectSystem=strict, PrivateTmp) : il voit /run/qemu-server, les
// journaux de vzdump et les stockages. La règle sudo ne nomme que ce script,
// mais vzdump accepte --script (un crochet lancé en root) : ce droit vaut root
// pour un agent compromis, comme le groupe docker.
const (
	VzdumpBinary     = "/usr/bin/vzdump"
	SystemdRunBinary = "/usr/bin/systemd-run"
	// sudoers.d, au nom du compte : un fichier par règle, qu'on retire seul.
	SudoersFile = "/etc/sudoers.d/" + brand.AgentAccount
	// Le drop-in de l'unité : NoNewPrivileges=true interdit tout sudo, c'est le
	// seul desserrement que ce droit demande.
	ProduceDropInDir  = "/etc/systemd/system/" + brand.AgentUnit + ".d"
	ProduceDropInFile = ProduceDropInDir + "/produce.conf"
	// Les archives en cours de production, dans l'état de l'agent.
	ProduceDir = brand.AgentStateDir + "/" + brand.AgentProduceDir
)

// VzdumpWrapperLines est le script de root, une ligne par élément. Aucune
// apostrophe : la préparation le passe à printf entre apostrophes.
func VzdumpWrapperLines() []string {
	return []string{
		"#!/bin/sh",
		"# " + brand.ProductName + " : vzdump hors du bac à sable de l agent, sur ordre de l agent.",
		"exec " + SystemdRunBinary + " --pipe --wait --collect --quiet " + VzdumpBinary + ` "$@"`,
	}
}

// SudoersRuleLines est la règle : le compte de l'agent lance le script, rien
// d'autre, sans mot de passe.
func SudoersRuleLines() []string {
	return []string{brand.AgentAccount + " ALL=(root) NOPASSWD: " + brand.VzdumpWrapperPath}
}

// ProduceDropInLines est le contenu du drop-in de l'unité.
func ProduceDropInLines() []string {
	return []string{"[Service]", "NoNewPrivileges=false"}
}
