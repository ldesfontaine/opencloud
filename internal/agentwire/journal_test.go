package agentwire

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/anchor"
)

func TestJournalFields_EveryOrderKindDeclaresItsList(t *testing.T) {
	for _, kind := range OrderKinds() {
		if _, ok := JournalFields(kind); !ok {
			t.Errorf("%s: no journal list declared (an empty list is a choice, a missing one is not)", kind)
		}
	}
	for name := range journalFields {
		if !Kind(name).IsOrder() {
			t.Errorf("%s: a journal list for something that is not an order kind", name)
		}
	}
}

// Un champ qui peut porter un secret ou un contenu n'entre jamais dans une
// liste blanche, quel que soit le genre.
func TestJournalFields_NeverNameASecretOrAContent(t *testing.T) {
	forbidden := []string{"secret", "password", "passphrase", "token", "private", "env", "compose", "certificate", "files", "key"}
	for name, fields := range journalFields {
		for _, field := range fields {
			for _, word := range forbidden {
				// public_key est la clé publique de l'agent.server_key : publique.
				if strings.Contains(field, word) && field != "public_key" {
					t.Errorf("%s: field %q looks like a secret or a content", name, field)
				}
			}
		}
	}
}

func TestJournalParams_KeepsOnlyTheListedPlainFields(t *testing.T) {
	params := json.RawMessage(`{"target":"abc","host":"10.0.0.2","port":8000,"user":"u","certificate":"-----BEGIN CERTIFICATE-----","rest_password":"hunter2","extra":{"a":1}}`)
	got := JournalParams(KindResticProvision, params)
	want := map[string]string{"target": "abc", "host": "10.0.0.2", "port": "8000", "user": "u"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("params %v, expected %v", got, want)
	}
	if got := JournalParams(KindAgentPing, params); got != nil {
		t.Fatalf("a kind with an empty list logs nothing: %v", got)
	}
	long := json.RawMessage(`{"path":"` + strings.Repeat("a", 300) + `","stamp":"line\nbreak"}`)
	if got := JournalParams(KindResticRestore, long); len(got) != 0 {
		t.Fatalf("a long or unprintable value is a content, not an identifier: %v", got)
	}
	if got := JournalParams(KindResticProvision, json.RawMessage(`not json`)); got != nil {
		t.Fatalf("unreadable parameters: %v", got)
	}
}

// Le registre des ancres nomme des ordres et des champs : ils existent.
func TestAnchorKinds_BoundRealOrderFields(t *testing.T) {
	structs := map[string]reflect.Type{
		string(KindBackupProduce):   reflect.TypeFor[BackupProduceParams](),
		string(KindResticProvision): reflect.TypeFor[ResticProvisionParams](),
	}
	for _, kind := range anchor.Kinds() {
		for _, bound := range kind.Bound {
			if !Kind(bound.Order).IsOrder() {
				t.Errorf("%s bounds %q, which is not an order kind", kind.Name, bound.Order)
			}
			params, known := structs[bound.Order]
			if !known {
				t.Errorf("%s: no parameters struct known for %s: add it to this test", kind.Name, bound.Order)
				continue
			}
			var tags []string
			for index := range params.NumField() {
				tags = append(tags, strings.Split(params.Field(index).Tag.Get("json"), ",")[0])
			}
			if !slices.Contains(tags, bound.Field) {
				t.Errorf("%s bounds %s.%s, which is not a JSON field of %s", kind.Name, bound.Order, bound.Field, params.Name())
			}
		}
	}
}

func TestAnchorRefusal_NamesTheKindAndIsAReportedReason(t *testing.T) {
	for _, kind := range anchor.Kinds() {
		for _, why := range []anchor.Why{anchor.WhyMissing, anchor.WhyUnreadable, anchor.WhyOutside} {
			reason := AnchorRefusal(anchor.Finding{Kind: kind, Why: why})
			if !strings.HasSuffix(reason, ":"+kind.Name) || !IsReportedReason(reason) {
				t.Errorf("%s %s: %q", kind.Name, why, reason)
			}
		}
	}
	for _, reason := range []string{"anchor_missing:nothing", "right_missing:nothing", "anchor_missing:"} {
		if IsReportedReason(reason) {
			t.Errorf("%q must not be a reported reason", reason)
		}
	}
	if !IsReportedReason(RightMissing("backup.produce")) || !IsReportedReason(ReasonBusy) {
		t.Error("plain and right reasons stay reported")
	}
}
