package agentwire

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Les agents installés lisent et écrivent ces étiquettes : le moindre
// changement les casse sans que rien d'autre ne le voie.
func TestWire_JSONLabelsAreFrozen(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"enroll request", EnrollRequest{MachineID: "m", PublicKey: "k", Token: "t", Hostname: "h", OS: "o", Arch: "a", AgentVersion: "v", HostFingerprint: "f"},
			`{"machine_id":"m","public_key":"k","token":"t","hostname":"h","os":"o","arch":"a","agent_version":"v","host_fingerprint":"f"}`},
		{"enroll request without fingerprint", EnrollRequest{MachineID: "m"},
			`{"machine_id":"m","public_key":"","token":"","hostname":"","os":"","arch":"","agent_version":""}`},
		{"enroll response", EnrollResponse{MachineID: "m", Name: "n", AgentID: "a", ServerKey: "k"}, `{"machine_id":"m","name":"n","agent_id":"a","server_key":"k"}`},
		{"empty signal", SignalRequest{}, `{}`},
		{"full signal", SignalRequest{
			Readings: []sampler.Reading{{SampledAt: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}},
			Services: &container.Report{Complete: true},
			Probes:   &probe.Report{},
			Images:   &update.Report{},
		}, `{"readings":[{"sampled_at":"2026-09-12T12:00:00Z","cpu_percent":0,"cpu_cores":0,"load_1":0,"mem_used":0,"mem_total":0,"swap_used":0,"swap_total":0,"disk_used":0,"disk_total":0,"net_rx_per_second":0,"net_tx_per_second":0}],"services":{"complete":true},"probes":{},"images":{}}`},
		{"actions section", SignalRequest{Actions: &ActionReport{Results: []ActionResult{{RunID: "r", State: ActionRefused, Reason: ReasonReplayed}}}},
			`{"actions":{"results":[{"run_id":"r","state":"refused","reason":"replayed"}]}}`},
		{"ssh keys section", SignalRequest{SSHKeys: &sshkeys.Report{Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateNone, Keys: []sshkeys.Key{}}}}},
			`{"ssh_keys":{"accounts":[{"name":"root","uid":0,"state":"none","truncated":false,"ignored":0,"keys":[]}],"truncated":false}}`},
		{"order body", OrderBody{RunID: "r", MachineID: "m", AgentID: "a", Kind: KindAgentPing, NotAfter: 1, Seq: 2},
			`{"run_id":"r","machine_id":"m","agent_id":"a","kind":"agent.ping","not_after":1,"seq":2}`},
		{"signed order", SignedOrder{Body: []byte("{}"), Signature: []byte{1}, Key: "SHA256:x"}, `{"body":"e30=","signature":"AQ==","key":"SHA256:x"}`},
		{"signal response", SignalResponse{Rejected: map[string]string{SectionProbes: SectionRefusal(SectionProbes)}}, `{"rejected":{"probes":"bad_probes"}}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.value)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// Le serveur écrit les sections dans cet ordre, et un agent lit « bad_services »
// pour la section des conteneurs : clés figées.
func TestSignalSections_KeepTheirOrderAndRefusalKeys(t *testing.T) {
	want := []string{"readings", "services", "probes", "images", "actions", "ssh_keys"}
	got := SignalSections()
	if len(got) != len(want) {
		t.Fatalf("sections %v", got)
	}
	for i, section := range want {
		if got[i] != section {
			t.Errorf("section %d = %q, want %q", i, got[i], section)
		}
		if SectionRefusal(section) != "bad_"+section {
			t.Errorf("refusal of %q = %q", section, SectionRefusal(section))
		}
	}
}

// Les en-têtes de la preuve signée sont ceux que les agents installés posent.
func TestWire_HeaderNamesAreFrozen(t *testing.T) {
	want := map[string]string{
		HeaderMachine:        "OpenCloud-Machine",
		HeaderNonce:          "OpenCloud-Nonce",
		HeaderTimestamp:      "OpenCloud-Timestamp",
		HeaderSignature:      "OpenCloud-Signature",
		HeaderAgentVersion:   "OpenCloud-Agent-Version",
		HeaderAgentOS:        "OpenCloud-Agent-OS",
		HeaderAgentRights:    "OpenCloud-Agent-Rights",
		HeaderAgentRoot:      "OpenCloud-Agent-Root",
		HeaderSignalSections: "OpenCloud-Signal-Sections",
		HeaderServerKey:      "OpenCloud-Server-Key",
		HeaderProtocol:       "OpenCloud-Agent-Protocol",
	}
	for got, name := range want {
		if got != name {
			t.Errorf("header %q, want %q", got, name)
		}
	}
}

// Les raisons d'indisponibilité d'un droit sont des raisons de refus : le
// front les traduit par les clés d'un refus. machine ne peut pas importer
// agentwire, la liste y est écrite à la main : elle se garde ici.
func TestUnavailableReasons_AreRefusalReasons(t *testing.T) {
	for _, reason := range machine.UnavailableReasons() {
		if !slices.Contains(RefusalReasons(), reason) {
			t.Errorf("%q n'est pas une raison de refus connue", reason)
		}
	}
	for _, reason := range []string{ReasonDockerDenied, ReasonDockerMissing, ReasonPathDenied, ReasonPathUnwritable, ReasonNotSelfUpdatable} {
		if !slices.Contains(machine.UnavailableReasons(), reason) {
			t.Errorf("%q manque à la liste des indisponibilités", reason)
		}
	}
}
