package container_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
)

func labelled(id, name string, labels map[string]string) container.Observed {
	observed := running(id, name, "")
	observed.Labels = labels
	return observed
}

// Une carte hors bornes perd ses labels, pas son conteneur : un seul fautif
// ne fait pas perdre l'inventaire. Le conteneur reste étiqueté, mal
// (Labels.Invalid) : il ne retombe pas en silence sur Compose ni sur le nom.
func TestValidate_DropsOutOfBoundsLabelsKeepsTheContainer(t *testing.T) {
	tooMany := map[string]string{}
	for index := range container.MaxLabelsPerContainer + 1 {
		tooMany[strings.Repeat("k", index+1)] = "x"
	}
	cases := map[string]map[string]string{
		"too many keys":          tooMany,
		"key too long":           {strings.Repeat("k", container.MaxLabelKeyLength+1): "x"},
		"empty key":              {"": "x"},
		"value too long":         {"service": strings.Repeat("v", container.MaxNameLength+1)},
		"valid with junk":        {"service": "svc_0123456789abcdef", "unit": "web", strings.Repeat("k", container.MaxLabelKeyLength+1): "x"},
		"service is not an id":   {"service": "web", "unit": "web"},
		"service with a space":   {"service": "svc_0123456789abcde ", "unit": "web"},
		"unit with a space":      {"service": "svc_0123456789abcdef", "unit": "my web"},
		"unit with a slash":      {"service": "svc_0123456789abcdef", "unit": "../web"},
		"control character":      {"service": "svc_0123456789abcdef", "unit": "web\n"},
		"control in another key": {"extra": "a\x00b"},
		"invalid utf-8":          {"extra": "a\xffb"},
	}
	for name, labels := range cases {
		t.Run(name, func(t *testing.T) {
			b := newBench(t)
			var journal bytes.Buffer
			b.tracker = container.New(b.db, slog.New(slog.NewTextHandler(&journal, nil)))
			b.tracker.SetClock(func() time.Time { return b.now })

			b.record(t, inventory(labelled(webID, "web", labels), labelled(dbID, "db", map[string]string{"service": "svc_00000000000000db", "unit": "db"})))

			if got := b.get(t, webID); got.Name != "web" || got.Labels != (container.Labels{Invalid: true}) || got.Labels.IsEmpty() {
				t.Fatalf("web = %+v, want the container kept, labelled and invalid", got)
			}
			if got := b.get(t, dbID); got.Labels != (container.Labels{Service: "svc_00000000000000db", Unit: "db"}) {
				t.Fatalf("db labels = %+v, one faulty container must not cost the others theirs", got.Labels)
			}
			if !strings.Contains(journal.String(), "labels of container web") {
				t.Fatalf("l'écart n'est pas journalisé : %s", journal.String())
			}
		})
	}
}

// La carte part entière, le serveur n'en garde que deux : un suffixe qu'une
// version future lira ne coûte rien à celle-ci.
func TestRecord_WritesTheLabelsOfEachContainer(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(
		labelled(webID, "web", map[string]string{"service": "svc_0123456789abcdef", "unit": "app", "futur": "ignoré"}),
		labelled(dbID, "db", map[string]string{"service": "svc_0123456789abcdef"}),
	))
	if got := b.get(t, webID).Labels; got != (container.Labels{Service: "svc_0123456789abcdef", Unit: "app"}) {
		t.Fatalf("web labels = %+v", got)
	}
	if got := b.get(t, dbID).Labels; got != (container.Labels{Service: "svc_0123456789abcdef"}) {
		t.Fatalf("db labels = %+v, an incomplete pair is stored as is", got)
	}
	// Le signe de l'agent (aucun label lisible, des labels écartés) est un
	// conteneur étiqueté, mal : ni service ni unité, mais pas « sans label ».
	b.record(t, inventory(labelled(webID, "web", map[string]string{"invalid": "2"}), running(dbID, "db", "")))
	if got := b.get(t, webID).Labels; got != (container.Labels{Invalid: true}) || got.IsEmpty() {
		t.Fatalf("web labels = %+v, want invalid and not empty", got)
	}
	// Le conteneur recréé sans label les perd : l'inventaire fait foi.
	b.record(t, inventory(running(webID, "web", ""), running(dbID, "db", "")))
	if got := b.get(t, webID).Labels; !got.IsEmpty() {
		t.Fatalf("web labels = %+v after an inventory without them", got)
	}
}

func TestRecord_EventWithoutContainerKeepsTheLabels(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(labelled(webID, "web", map[string]string{"service": "svc_0123456789abcdef", "unit": "app"})))
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: "health_status", ContainerID: webID, Health: container.HealthHealthy}}})
	if got := b.get(t, webID).Labels; got != (container.Labels{Service: "svc_0123456789abcdef", Unit: "app"}) {
		t.Fatalf("labels after an event without a container = %+v", got)
	}
	// Un événement qui porte le conteneur réinspecté dit les labels du moment.
	b.record(t, container.Report{Events: []container.Event{{
		At: testNow, Action: "start", ContainerID: webID, State: container.StateRunning,
		Container: func() *container.Observed {
			observed := labelled(webID, "web", map[string]string{"service": "svc_0123456789abcdef", "unit": "worker"})
			return &observed
		}(),
	}}})
	if got := b.get(t, webID).Labels.Unit; got != "worker" {
		t.Fatalf("unit after a re-inspected event = %q, want worker", got)
	}
}

func TestMatchLevel_LabelIsNotWeak(t *testing.T) {
	if container.MatchLabel.IsWeak() || container.MatchCompose.IsWeak() || !container.MatchName.IsWeak() {
		t.Fatal("seul le nom du conteneur est un rattachement faible")
	}
	levels := container.MatchLevels()
	if len(levels) != 3 || levels[0] != container.MatchLabel || levels[1] != container.MatchCompose || levels[2] != container.MatchName {
		t.Fatalf("levels = %v, want label, compose, name", levels)
	}
}
