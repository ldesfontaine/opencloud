package link

import (
	"testing"
	"time"
)

func TestKeys_NameTheKindAndCarryTheName(t *testing.T) {
	for key, label := range map[string]string{
		ComposeKey("edge", "traefik"):      "edge/traefik",
		ContainerKey("redis"):              "redis",
		SystemdKey("forgejo.service"):      "forgejo.service",
		"sans-prefixe":                     "sans-prefixe",
		ComposeKey("shop", "app") + "/odd": "shop/app/odd",
	} {
		if got := LabelOf(key); got != label {
			t.Errorf("LabelOf(%q) = %q, attendu %q", key, got, label)
		}
	}
	if !validKey(ComposeKey("edge", "traefik")) || validKey("sans-prefixe") || validKey("container:") || validKey("container:a\nb") {
		t.Error("une clé a un préfixe connu, un nom, et rien d'invisible")
	}
}

func TestDefinition_Validate(t *testing.T) {
	a := Endpoint{MachineID: "m1", Key: ContainerKey("web")}
	b := Endpoint{MachineID: "m1", Key: ContainerKey("db")}
	cases := map[string]struct {
		definition Definition
		want       error
	}{
		"valide":                  {Definition{From: a, To: b, Port: 5432}, nil},
		"sans port":               {Definition{From: a, To: b}, nil},
		"soi-même":                {Definition{From: a, To: a}, ErrSelf},
		"même clé, autre machine": {Definition{From: a, To: Endpoint{MachineID: "m2", Key: a.Key}}, nil},
		"clé illisible":           {Definition{From: Endpoint{MachineID: "m1", Key: "x"}, To: b}, ErrInvalidKey},
		"machine vide":            {Definition{From: Endpoint{Key: a.Key}, To: b}, ErrInvalidKey},
		"port trop grand":         {Definition{From: a, To: b, Port: 70000}, ErrInvalidPort},
		"port négatif":            {Definition{From: a, To: b, Port: -1}, ErrInvalidPort},
	}
	for name, tc := range cases {
		if got := tc.definition.Validate(); got != tc.want {
			t.Errorf("%s : %v, attendu %v", name, got, tc.want)
		}
	}
}

func TestLink_FreshnessAndRemovability(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-StaleAfter - time.Hour)
	tool := Link{Signal: SignalDependsOn, LastSeen: old}
	operator := Link{Signal: SignalOperator, LastSeen: old}
	if !tool.Stale(now) || tool.Removable() {
		t.Error("un lien d'outil non revu depuis plus de 7 jours est ancien, et ne se retire pas ici")
	}
	if operator.Stale(now) || !operator.Removable() {
		t.Error("un lien écrit par l'opérateur ne vieillit pas, et se retire")
	}
	proposed := Link{ID: DeducedPrefix + "ab12", Proof: ProofDeduced, Signal: SignalPublicURL}
	if !proposed.Proposed() || proposed.Stale(now) {
		t.Error("une route proposée n'a pas de dernière vue : elle n'est jamais ancienne")
	}
	environment := Link{ID: ServicePrefix + "x", Nature: NatureConfig, Proof: ProofDeduced, Signal: SignalEnvironment, LastSeen: old}
	if environment.Proposed() || environment.Dismissed() || environment.Removable() || !environment.Stale(now) {
		t.Error("un lien lu dans l'environnement est écrit, déduit, non écarté, ne se retire pas ici et vieillit")
	}
	if (Link{Signal: SignalContainerLink, LastSeen: now.Add(-StaleAfter)}).Stale(now) {
		t.Error("pile 7 jours n'est pas encore ancien")
	}
}

func TestLink_ADeducedRowIsTheTraceOfADismissal(t *testing.T) {
	dismissed := Link{Proof: ProofDeduced, Signal: SignalOperator, Nature: NatureRoute}
	confirmed := Link{Proof: ProofDeclared, Signal: SignalOperator, Nature: NatureRoute}
	if !dismissed.Dismissed() || dismissed.Removable() {
		t.Error("une route écartée se rétablit, elle ne se retire pas")
	}
	if confirmed.Dismissed() || !confirmed.Removable() {
		t.Error("une route confirmée est un lien de l'opérateur")
	}
}

func TestDeducedID_IsTheSameForTheSamePairOnly(t *testing.T) {
	a := Endpoint{MachineID: "m1", Key: ContainerKey("caddy")}
	b := Endpoint{MachineID: "m2", Key: ContainerKey("blog")}
	first := DeducedID(a, b, NatureRoute)
	if again := DeducedID(a, b, NatureRoute); again != first || first == DeducedID(b, a, NatureRoute) || first == DeducedID(a, b, NatureConfig) {
		t.Error("l'identifiant se refait de la paire, dans son sens et sa nature")
	}
}

func TestSeen_TheSignalSaysNatureAndProof(t *testing.T) {
	cases := map[Signal]struct {
		nature   Nature
		proof    Proof
		writable bool
	}{
		SignalDependsOn:     {NatureOrder, ProofDeclared, true},
		SignalContainerLink: {NatureOrder, ProofDeclared, true},
		SignalEnvironment:   {NatureConfig, ProofDeduced, true},
		SignalOperator:      {"", "", false},
		SignalPublicURL:     {"", "", false},
	}
	for signal, want := range cases {
		nature, proof, writable := (Seen{Signal: signal}).Kind()
		if nature != want.nature || proof != want.proof || writable != want.writable {
			t.Errorf("%s : %s %s %v, attendu %s %s %v", signal, nature, proof, writable, want.nature, want.proof, want.writable)
		}
	}
}
