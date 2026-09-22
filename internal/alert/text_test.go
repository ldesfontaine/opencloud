package alert_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Chaque type du catalogue a ses trois lignes dans chaque langue, sans
// clé manquante ni verbe de format oublié.
func TestDescribe_EveryKindInEveryLanguage(t *testing.T) {
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	details := alert.Details{Count: 4, Percent: 86, MountPoint: "/data", ExitCode: 137, Reason: "refused", Target: "cloud.exemple.fr", NotAfter: testNow.Add(12 * 24 * time.Hour), Since: testNow.Add(-3 * time.Minute)}
	for _, code := range lang.Codes() {
		for _, kind := range alert.Kinds() {
			for _, severity := range []alert.Severity{alert.SeverityAttention, alert.SeverityDanger} {
				found := alert.Alert{Kind: kind, Severity: severity, Object: alert.Object{Name: "nextcloud"}, MachineName: "vps-paris-1", Details: details}
				if kind == alert.KindCertUntrusted {
					found.Details.Reason = alert.ReasonChain
				}
				text := alert.Describe(catalogs.For(code), found, testNow)
				for _, line := range []string{text.Fact, text.Cause, text.Action} {
					if line == "" || strings.Contains(line, "[") || strings.Contains(line, "%d") || strings.Contains(line, "%s") || strings.Contains(line, "%!") {
						t.Errorf("%s %s %s: %q", code, kind, severity, line)
					}
				}
			}
		}
	}
}

// Les exemples de la direction artistique, mot pour mot.
func TestDescribe_ReadsLikeTheCanvas(t *testing.T) {
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	fr := catalogs.For(lang.French)
	disk := alert.Describe(fr, alert.Alert{Kind: alert.KindDiskFull, Severity: alert.SeverityAttention, Object: alert.Object{Name: "/data"}, MachineName: "vps-lyon-2", Details: alert.Details{MountPoint: "/data", Percent: 86}}, testNow)
	if disk.Fact != "Disque à 86 %" || disk.Cause != "/data sur vps-lyon-2. Seuil d'alerte fixé à 85 %." {
		t.Fatalf("disk %+v", disk)
	}
	expiring := alert.Describe(fr, alert.Alert{Kind: alert.KindCertExpiring, Severity: alert.SeverityAttention, Details: alert.Details{Target: "blog.desfontaine.fr", NotAfter: testNow.Add(12*24*time.Hour - time.Hour)}}, testNow)
	if expiring.Fact != "Certificat qui expire" || expiring.Cause != "blog.desfontaine.fr, dans 12 jours." {
		t.Fatalf("expiring %+v", expiring)
	}
	lost := alert.Describe(fr, alert.Alert{Kind: alert.KindMachineLost, Severity: alert.SeverityDanger, Object: alert.Object{Name: "vps-lyon-2"}, Details: alert.Details{Since: testNow.Add(-3 * time.Minute)}}, testNow)
	if lost.Fact != "Machine perdue" || lost.Cause != "vps-lyon-2 ne donne plus signe de vie depuis 3 min." {
		t.Fatalf("lost %+v", lost)
	}
	loop := alert.Describe(fr, alert.Alert{Kind: alert.KindServiceRestart, Severity: alert.SeverityDanger, Object: alert.Object{Name: "nextcloud"}, MachineName: "vps-paris-1", Details: alert.Details{Count: 4, ExitCode: 2}}, testNow)
	if loop.Fact != "Boucle de redémarrage" || !strings.Contains(loop.Cause, "4 redémarrages") {
		t.Fatalf("loop %+v", loop)
	}
	noMachine := alert.Describe(fr, alert.Alert{Kind: alert.KindServiceDown, Severity: alert.SeverityDanger, Object: alert.Object{Name: "nextcloud"}, Details: alert.Details{ExitCode: 1}}, testNow)
	if !strings.Contains(noMachine.Cause, "nextcloud sur —") {
		t.Fatalf("no machine %+v", noMachine)
	}
}
