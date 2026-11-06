package machine

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// fitCase est une ligne de testdata/right_fit_cases.json, que le front relit
// (web/src/lib/rights.test.ts) : les deux jugements doivent dire la même chose.
type fitCase struct {
	Name  string `json:"name"`
	Agent *struct {
		Revoked      bool `json:"revoked"`
		Reported     bool `json:"reported"`
		Capabilities []struct {
			Name        string   `json:"name"`
			Paths       []string `json:"paths"`
			Unavailable string   `json:"unavailable"`
		} `json:"capabilities"`
	} `json:"agent"`
	Right  string `json:"right"`
	Expect struct {
		State  RightState `json:"state"`
		Reason string     `json:"reason"`
		Path   string     `json:"path"`
	} `json:"expect"`
}

func (c fitCase) agent() Agent {
	if c.Agent == nil {
		return Agent{}
	}
	agent := Agent{ID: "agt_test", Unavailable: map[string]string{}}
	if c.Agent.Revoked {
		agent.RevokedAt = time.Unix(1, 0)
	}
	if c.Agent.Reported {
		agent.ReportedAt = time.Unix(2, 0)
	}
	for _, capability := range c.Agent.Capabilities {
		agent.Capabilities = append(agent.Capabilities, Capability{Name: capability.Name, Paths: capability.Paths})
		if capability.Unavailable != "" {
			agent.Unavailable[capability.Name] = capability.Unavailable
		}
	}
	return agent
}

func TestFit_SharedCases_MatchTheirExpectation(t *testing.T) {
	raw, err := os.ReadFile("testdata/right_fit_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []fitCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no case: the test no longer sees the table")
	}
	for _, c := range cases {
		fit := c.agent().Fit(c.Right, "")
		if fit.State != c.Expect.State || fit.Reason != c.Expect.Reason || fit.Path != c.Expect.Path {
			t.Errorf("%s: got %+v, want %+v", c.Name, fit, c.Expect)
		}
	}
}

func TestFit_Scope_IsJudgedByPathOrByProducer(t *testing.T) {
	agent := Agent{ID: "agt_test", ReportedAt: time.Unix(2, 0), Capabilities: []Capability{
		{Name: CapabilityBackupReceive, Paths: []string{"/srv/backups"}},
		{Name: CapabilityBackupProduce, Paths: []string{ProducerProxmoxVzdump}},
	}}
	cases := []struct {
		name  string
		right string
		scope string
		want  RightState
	}{
		{"the folder itself", CapabilityBackupReceive, "/srv/backups", RightOK},
		{"under the folder", CapabilityBackupReceive, "/srv/backups/db", RightOK},
		{"a sibling with the same prefix", CapabilityBackupReceive, "/srv/backups2", RightMissing},
		{"a path that climbs out", CapabilityBackupReceive, "/srv/backups/../etc", RightMissing},
		{"the producer the right names", CapabilityBackupProduce, ProducerProxmoxVzdump, RightOK},
		{"a producer it does not name", CapabilityBackupProduce, "other", RightMissing},
		{"no scope: the right alone counts", CapabilityBackupReceive, "", RightOK},
	}
	for _, c := range cases {
		if got := agent.Fit(c.right, c.scope).State; got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestFit_ProduceRight_DoesNotReportAProducerAsAFolder(t *testing.T) {
	agent := Agent{ID: "agt_test", ReportedAt: time.Unix(2, 0), Capabilities: []Capability{{Name: CapabilityBackupProduce, Paths: []string{ProducerProxmoxVzdump}}}}
	if path := agent.Fit(CapabilityBackupProduce, ProducerProxmoxVzdump).Path; path != "" {
		t.Errorf("path = %q, want none", path)
	}
}
