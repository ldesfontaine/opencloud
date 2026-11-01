package firewall

import "strings"

// KnownMachine est une machine d'openCloud, réduite à ce qui la reconnaît dans
// un bail : l'adresse par laquelle elle se présente et ses cartes réseau.
type KnownMachine struct {
	ID   string
	Name string
	// Host : l'adresse sans port ; vide si on ne la connaît pas.
	Host string
	// MACs : en minuscules, deux-points compris.
	MACs []string
}

// MatchMachine rattache un bail à la machine qu'il désigne. L'adresse MAC
// l'emporte : elle suit la carte, quand l'adresse IP, elle, a pu changer. À
// défaut, l'adresse IP. Faux quand aucune machine ne correspond, ou quand
// deux machines se disputent l'adresse : mieux vaut ne rien dire que le dire
// de la mauvaise.
func MatchMachine(lease Lease, machines []KnownMachine) (KnownMachine, bool) {
	if mac := strings.ToLower(lease.MAC); mac != "" {
		if found, ok := uniqueMatch(machines, func(machine KnownMachine) bool { return hasMAC(machine, mac) }); ok {
			return found, true
		}
	}
	if lease.Address == "" {
		return KnownMachine{}, false
	}
	return uniqueMatch(machines, func(machine KnownMachine) bool { return machine.Host == lease.Address })
}

func hasMAC(machine KnownMachine, mac string) bool {
	for _, candidate := range machine.MACs {
		if candidate == mac {
			return true
		}
	}
	return false
}

func uniqueMatch(machines []KnownMachine, matches func(KnownMachine) bool) (KnownMachine, bool) {
	var found KnownMachine
	count := 0
	for _, machine := range machines {
		if matches(machine) {
			found = machine
			count++
		}
	}
	if count != 1 {
		return KnownMachine{}, false
	}
	return found, true
}
