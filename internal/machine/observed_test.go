package machine

import (
	"context"
	"testing"
	"time"
)

func statusSeen(active bool, seen time.Time) Status {
	agent := Agent{ID: "agt_x"}
	if !active {
		agent.RevokedAt = seen
	}
	return Status{Machine: Machine{Agent: agent, LastSeenAt: seen}}
}

// Observée : un agent actif dont le dernier signal a LostAfter au plus,
// borne comprise. Le flux ouvert (Online) n'y change rien.
func TestObserved_FollowsTheLastSignalOfAnActiveAgent(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name   string
		status Status
		want   bool
	}{
		{"signal à l'instant", statusSeen(true, now), true},
		{"signal à 1 min 50", statusSeen(true, now.Add(-110*time.Second)), true},
		{"signal à la borne", statusSeen(true, now.Add(-LostAfter)), true},
		{"signal à 2 min 10", statusSeen(true, now.Add(-130*time.Second)), false},
		{"jamais vue", statusSeen(true, time.Time{}), false},
		{"agent révoqué, signal frais", statusSeen(false, now), false},
		{"sans agent", Status{Machine: Machine{LastSeenAt: now}}, false},
	}
	for _, c := range cases {
		if got := Observed(c.status, now); got != c.want {
			t.Errorf("%s : %v, attendu %v", c.name, got, c.want)
		}
		withStream := c.status
		withStream.Online = true
		if got := Observed(withStream, now); got != c.want {
			t.Errorf("%s, flux ouvert : %v, attendu %v", c.name, got, c.want)
		}
	}
}

// Une machine coupée à chaud garde son flux ouvert : l'alerte se juge sur
// l'âge du dernier signal, pas sur le flux.
func TestCheckLost_JudgesTheLastSignalNotTheOpenStream(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	alerts := &lostAlerter{}
	h.service.SetAlerter(alerts)
	enrolled, _ := h.enroll(t, "vps-paris-1")
	session, err := h.service.Connect(ctx, enrolled, "192.0.2.1", "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	h.now = h.now.Add(110 * time.Second)
	if err := h.service.CheckLost(ctx); err != nil || len(alerts.lost) != 0 {
		t.Fatalf("à 1 min 50 sans signal : %v %v", alerts.lost, err)
	}
	h.now = h.now.Add(20 * time.Second)
	if err := h.service.CheckLost(ctx); err != nil || len(alerts.lost) != 1 {
		t.Fatalf("à 2 min 10 sans signal, flux ouvert : %v %v", alerts.lost, err)
	}
	if status, _ := h.service.Get(ctx, enrolled.ID); !status.Online {
		t.Fatal("la pastille « En ligne » suit le flux, pas cette règle")
	}

	// Les signaux reprennent sur le même flux : la machine est de retour
	// sans reconnexion, l'alerte se résout à ce passage.
	alerts.back = nil
	if _, err := h.service.Signal(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if err := h.service.CheckLost(ctx); err != nil || len(alerts.lost) != 1 || len(alerts.back) != 1 || alerts.back[0] != enrolled.ID {
		t.Fatalf("après un signal : perdues %v, revenues %v, %v", alerts.lost, alerts.back, err)
	}
}
