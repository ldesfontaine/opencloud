package service

import (
	"sort"
	"strconv"
)

// Un constat d'exposition est un fait calculé à la lecture, jamais stocké,
// jamais alerté : la feature alertes le reprendra avec son diff par type.
// Les règles viennent de l'analyse de Projet_M ; le mode host court-circuite
// l'analyse des ports, qui n'ont plus de sens sans espace réseau propre.
type FindingKind string

const (
	FindingHostNetwork        FindingKind = "host_network"
	FindingPrivileged         FindingKind = "privileged"
	FindingDatabasePortPublic FindingKind = "database_port_public"
	FindingPortPublic         FindingKind = "port_public"
)

func FindingKinds() []FindingKind {
	return []FindingKind{FindingHostNetwork, FindingPrivileged, FindingDatabasePortPublic, FindingPortPublic}
}

// Level dit ce que le front en fait : un point orange sur le nœud, ou une
// ligne d'information seulement. Un port publié sur toutes les interfaces
// est le cas normal d'un service web sans proxy : il s'écrit, sans alarmer.
type Level string

const (
	LevelInfo Level = "info"
	LevelWarn Level = "warn"
)

type Finding struct {
	Kind     FindingKind `json:"kind"`
	Level    Level       `json:"level"`
	Port     int         `json:"port,omitempty"`
	Protocol string      `json:"protocol,omitempty"`
}

// Les ports de base de données que personne ne devrait publier sur toutes
// les interfaces : MySQL et MariaDB, PostgreSQL, Redis, MongoDB. C'est le
// port du conteneur qui compte, pas celui de l'hôte.
var databasePorts = map[int]bool{3306: true, 5432: true, 6379: true, 27017: true}

// Exposure rend les constats d'un service, dans un ordre stable.
func Exposure(s Service) []Finding {
	findings := []Finding{}
	if s.Privileged {
		findings = append(findings, Finding{Kind: FindingPrivileged, Level: LevelWarn})
	}
	if s.NetworkMode == "host" {
		findings = append(findings, Finding{Kind: FindingHostNetwork, Level: LevelWarn})
		return findings
	}
	seen := map[string]bool{}
	for _, port := range s.Ports {
		key := port.Protocol + ":" + strconv.Itoa(port.ContainerPort)
		if !port.IsPublic() || seen[key] {
			continue
		}
		seen[key] = true
		finding := Finding{Kind: FindingPortPublic, Level: LevelInfo, Port: port.ContainerPort, Protocol: port.Protocol}
		if databasePorts[port.ContainerPort] {
			finding.Kind, finding.Level = FindingDatabasePortPublic, LevelWarn
		}
		findings = append(findings, finding)
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Port != findings[j].Port {
			return findings[i].Port < findings[j].Port
		}
		return findings[i].Protocol < findings[j].Protocol
	})
	return findings
}

// NeedsCaution dit si au moins un constat est au niveau attention : le
// point orange du nœud.
func NeedsCaution(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Level == LevelWarn {
			return true
		}
	}
	return false
}
