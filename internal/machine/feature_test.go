package machine

import "testing"

func TestSupports_EachFeature_ReadsItsOwnProof(t *testing.T) {
	cases := []struct {
		name    string
		agent   Agent
		feature Feature
		want    bool
	}{
		{"no rollback reported", Agent{}, FeatureAgentRollback, false},
		{"agent rollback reported", Agent{RollbackVersion: 1}, FeatureAgentRollback, true},
		{"agent rollback is not the service one", Agent{RollbackVersion: 1}, FeatureServiceRollback, false},
		{"service rollback reported", Agent{ServiceRollbackVersion: 1}, FeatureServiceRollback, true},
		{"prefix: the first version", Agent{Version: PrunePrefixSinceVersion}, FeaturePrunePrefix, true},
		{"prefix: a newer version", Agent{Version: "v0.10.0"}, FeaturePrunePrefix, true},
		{"prefix: an older version", Agent{Version: "v0.1.9"}, FeaturePrunePrefix, false},
		{"prefix: an unreadable version counts as older", Agent{Version: "dev"}, FeaturePrunePrefix, false},
		{"prefix: no version", Agent{}, FeaturePrunePrefix, false},
		{"an unknown feature is not supported", Agent{RollbackVersion: 1, Version: "v9.0.0"}, Feature("other"), false},
	}
	for _, c := range cases {
		if got := c.agent.Supports(c.feature); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
