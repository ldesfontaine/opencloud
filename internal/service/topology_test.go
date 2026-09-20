package service

import (
	"strings"
	"testing"
)

const (
	frontNet = "834fd73b3e7c0000c3adeedade3f9823efe78ea4fc3f7e162e89e879f23a5f6e"
	backNet  = "0af01ef1573714129ced483d26f2250c1de6ce40a2ae2b7911ecc9534c02c3bf"
)

func attached(networkID, name string) Attachment {
	return Attachment{NetworkID: networkID, Name: name, IP: "172.20.0.2"}
}

// Le projet de la planche : web sur deux réseaux et publié, sa base sur le
// réseau interne, un cache sur les deux avec un port de base de données
// publié sur toutes les interfaces.
func fixtureServices() []Service {
	return []Service{
		{ID: "m:web", Name: "web", Group: "ocfix", NetworkMode: "ocfix_back",
			Ports:     []Port{{IP: "0.0.0.0", HostPort: 18081, ContainerPort: 80, Protocol: "tcp"}, {IP: "::", HostPort: 18081, ContainerPort: 80, Protocol: "tcp"}},
			Networks:  []Attachment{attached(backNet, "ocfix_back"), attached(frontNet, "ocfix_front")},
			DependsOn: []Dependency{{Name: "cache", Source: DependencyCompose}, {Name: "db", Source: DependencyCompose}, {Name: "ghost", Source: DependencyCompose}}},
		{ID: "m:db", Name: "db", Group: "ocfix", NetworkMode: "ocfix_back",
			Ports:    []Port{{IP: "127.0.0.1", HostPort: 15432, ContainerPort: 5432, Protocol: "tcp"}},
			Networks: []Attachment{attached(backNet, "ocfix_back")}},
		{ID: "m:cache", Name: "cache", Group: "ocfix", NetworkMode: "ocfix_back",
			Ports:    []Port{{IP: "0.0.0.0", HostPort: 16379, ContainerPort: 6379, Protocol: "tcp"}},
			Networks: []Attachment{attached(backNet, "ocfix_back"), attached(frontNet, "ocfix_front")}},
		{ID: "m:lonely", Name: "lonely", NetworkMode: "bridge", Networks: []Attachment{attached(strings.Repeat("d", 64), "bridge")}},
	}
}

func fixtureNetworks() []Network {
	return []Network{
		{NetworkID: strings.Repeat("d", 64), Name: "bridge", Driver: "bridge"},
		{NetworkID: strings.Repeat("e", 64), Name: "host", Driver: "host"},
		{NetworkID: strings.Repeat("f", 64), Name: "none", Driver: "null"},
		{NetworkID: backNet, Name: "ocfix_back", Driver: "bridge", Internal: true, Group: "ocfix"},
		{NetworkID: frontNet, Name: "ocfix_front", Driver: "bridge", Group: "ocfix"},
		{NetworkID: strings.Repeat("a", 64), Name: "empty_net", Driver: "bridge"},
	}
}

func TestBuildTopology_GroupsByUserNetworkAndPlacesWhereMostNeighbours(t *testing.T) {
	topology := BuildTopology(fixtureServices(), fixtureNetworks())
	// bridge, host, none et le réseau vide ne font pas de groupe.
	if len(topology.Groups) != 1 {
		t.Fatalf("groups = %+v", topology.Groups)
	}
	back := topology.Groups[0]
	if back.Name != "ocfix_back" || !back.Internal || strings.Join(back.Members, ",") != "m:web,m:db,m:cache" {
		t.Fatalf("back = %+v", back)
	}
}

func TestBuildTopology_TieGoesToTheFirstNetworkByName(t *testing.T) {
	services := []Service{
		{ID: "m:a", Name: "a", Networks: []Attachment{attached(frontNet, "zeta"), attached(backNet, "alpha")}},
		{ID: "m:b", Name: "b", Networks: []Attachment{attached(backNet, "alpha")}},
		{ID: "m:c", Name: "c", Networks: []Attachment{attached(frontNet, "zeta")}},
	}
	topology := BuildTopology(services, []Network{
		{NetworkID: backNet, Name: "alpha", Driver: "bridge"},
		{NetworkID: frontNet, Name: "zeta", Driver: "bridge"},
	})
	if len(topology.Groups) != 2 || topology.Groups[0].Name != "alpha" || strings.Join(topology.Groups[0].Members, ",") != "m:a,m:b" || strings.Join(topology.Groups[1].Members, ",") != "m:c" {
		t.Fatalf("groups = %+v", topology.Groups)
	}
}

func TestBuildTopology_ANetworkNotListedStillGroupsItsMembers(t *testing.T) {
	services := []Service{{ID: "m:a", Name: "a", Networks: []Attachment{attached(backNet, "late_net")}}}
	topology := BuildTopology(services, nil)
	if len(topology.Groups) != 1 || topology.Groups[0].Name != "late_net" || topology.Groups[0].Internal {
		t.Fatalf("groups = %+v", topology.Groups)
	}
}

func TestBuildTopology_PublicEdgesDedupeIPv4AndIPv6(t *testing.T) {
	topology := BuildTopology(fixtureServices(), fixtureNetworks())
	var public []Edge
	for _, edge := range topology.Edges {
		if edge.Kind == EdgePublic {
			public = append(public, edge)
		}
	}
	if len(public) != 2 || public[0] != (Edge{From: InternetNode, To: "m:web", Kind: EdgePublic, Port: 18081, Protocol: "tcp"}) || public[1].To != "m:cache" {
		t.Fatalf("public = %+v", public)
	}
}

func TestBuildTopology_DependenciesResolveInTheProjectAndUnknownOnesHaveNoEdge(t *testing.T) {
	topology := BuildTopology(fixtureServices(), fixtureNetworks())
	var depends []string
	for _, edge := range topology.Edges {
		if edge.Kind == EdgeDepends {
			depends = append(depends, edge.From+">"+edge.To+":"+string(edge.Source))
		}
	}
	if strings.Join(depends, " ") != "m:web>m:cache:compose m:web>m:db:compose" {
		t.Fatalf("depends = %v", depends)
	}
}

func TestBuildTopology_AComposeDependencyDoesNotCrossProjects(t *testing.T) {
	services := []Service{
		{ID: "m:web", Name: "web", Group: "one", DependsOn: []Dependency{{Name: "db", Source: DependencyCompose}, {Name: "shared-db", Source: DependencyLink}}},
		{ID: "m:db", Name: "db", Group: "two"},
		{ID: "m:shared", Name: "shared-db"},
	}
	topology := BuildTopology(services, nil)
	if len(topology.Edges) != 1 || topology.Edges[0].To != "m:shared" || topology.Edges[0].Source != DependencyLink {
		t.Fatalf("edges = %+v", topology.Edges)
	}
}

func TestExposure_FollowsTheFourRules(t *testing.T) {
	services := fixtureServices()
	if got := Exposure(services[0]); len(got) != 1 || got[0] != (Finding{Kind: FindingPortPublic, Level: LevelInfo, Port: 80, Protocol: "tcp"}) {
		t.Fatalf("web = %+v", got)
	}
	if got := Exposure(services[1]); len(got) != 0 {
		t.Fatalf("db on loopback = %+v", got)
	}
	if got := Exposure(services[2]); len(got) != 1 || got[0].Kind != FindingDatabasePortPublic || got[0].Level != LevelWarn || got[0].Port != 6379 || !NeedsCaution(got) {
		t.Fatalf("cache = %+v", got)
	}
	host := Service{NetworkMode: "host", Privileged: true, Ports: []Port{{IP: "0.0.0.0", HostPort: 5432, ContainerPort: 5432, Protocol: "tcp"}}}
	if got := Exposure(host); len(got) != 2 || got[0].Kind != FindingPrivileged || got[1].Kind != FindingHostNetwork {
		t.Fatalf("host = %+v", got)
	}
	if got := Exposure(Service{}); len(got) != 0 || NeedsCaution(got) {
		t.Fatalf("clean = %+v", got)
	}
}
