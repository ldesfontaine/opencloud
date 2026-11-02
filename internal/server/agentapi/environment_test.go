package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/link"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

// Le serveur borne l'environnement comme les labels : « un agent n'est pas une
// preuve ». Sans observe.env, les instances désignées par l'environnement sont
// retirées du rapport ; avec lui, elles deviennent des liens. Dans les deux
// cas, rien d'une valeur secrète que l'agent aurait glissée dans le signal ne
// se retrouve dans la base (banc de non-fuite, côté serveur).

const (
	webID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dbID  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var forgedSecrets = []string{"hunter2-Zq83", "tok_9f3a77c1d2e8", "p4ss-Xk29", "DB_PASSWORD"}

// environmentSignal : un inventaire de deux conteneurs, web désignant db par
// son environnement, avec en plus des champs qu'un agent mal écrit (ou
// hostile) enverrait : l'environnement brut, un nom de variable, une valeur.
func environmentSignal() map[string]any {
	created := servertest.Now().Add(-time.Hour).Format(time.RFC3339)
	web := map[string]any{
		"container_id": webID, "name": "web", "group": "shop", "compose_service": "web", "image": "img", "state": "running", "created_at": created,
		"env_targets": []map[string]any{{"name": "db", "group": "shop", "port": 5432}, {"name": "ghost", "group": "shop"}},
		"env":         []string{"DB_PASSWORD=hunter2-Zq83", "API_TOKEN=tok_9f3a77c1d2e8"},
		"environment": "postgres://svc:p4ss-Xk29@db/app",
	}
	db := map[string]any{"container_id": dbID, "name": "db", "group": "shop", "compose_service": "db", "image": "img", "state": "running", "created_at": created}
	return map[string]any{"services": map[string]any{"complete": true, "inventory": []any{web, db}}}
}

func (b *bench) signalAs(t *testing.T, session string, payload any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/agent/signal", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	b.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", recorder.Code, recorder.Body.String())
	}
}

func (b *bench) environmentLinks(t *testing.T) []link.Link {
	t.Helper()
	all, err := link.New(b.db, b.logger).List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func TestSignal_EnvironmentLinksNeedTheRightAndNothingSecretIsStored(t *testing.T) {
	cases := map[string]struct {
		rights string
		want   int
	}{
		"without observe.env the instances are dropped": {"observe,probe.lan", 0},
		"with observe.env they become links":            {"observe,observe.env", 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := newBench(t)
			server.containers.SetLinks(link.New(server.db, server.logger))
			enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
			resp, session, _, cancel := servertest.OpenStreamWithRights(t, server, enrolled.ID, private, tc.rights)
			defer resp.Body.Close()
			defer cancel()

			server.signalAs(t, session, environmentSignal())

			found := server.environmentLinks(t)
			if len(found) != tc.want {
				t.Fatalf("%d link(s), want %d: %+v", len(found), tc.want, found)
			}
			if tc.want == 1 {
				got := found[0]
				if got.From.Key != link.ComposeKey("shop", "web") || got.To.Key != link.ComposeKey("shop", "db") || got.Port != 5432 ||
					got.Signal != link.SignalEnvironment || got.Proof != link.ProofDeduced || got.Nature != link.NatureConfig {
					t.Errorf("link = %+v (an unknown target like « ghost » gives nothing)", got)
				}
			}

			// Toute la base, octet par octet : aucune valeur ni aucun nom de variable.
			snapshot := filepath.Join(t.TempDir(), "snapshot.db")
			if err := server.db.SnapshotDatabase(context.Background(), snapshot); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range forgedSecrets {
				if strings.Contains(string(content), secret) {
					t.Errorf("%q is in the database", secret)
				}
			}
		})
	}
}
