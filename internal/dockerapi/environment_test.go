package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// Les valeurs semées : aucune ne doit ressortir de EnvHosts, sous aucune forme.
const (
	seededPassword = "hunter2-Zq83"
	seededToken    = "tok_9f3a77c1d2e8"
	seededUserinfo = "svc-user:p4ss-Xk29"
	seededQuery    = "qtok_77aa31"
)

func seededEnvironment(t *testing.T) []byte {
	t.Helper()
	entries := []string{
		"DB_PASSWORD=" + seededPassword,
		"API_TOKEN=" + seededToken,
		"DATABASE_URL=postgres://" + seededUserinfo + "@db:5432/app?sslmode=disable&token=" + seededQuery,
		"REDIS_URL=redis://cache:6379/0",
		"CACHE_HOST=Cache",
		"JDBC=jdbc:postgresql://pg.internal:5433/x",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/bin",
		"MESSAGE=hello world",
		"EMPTY=",
		"NO_EQUALS",
		"LONG=" + strings.Repeat("a", maxEnvValue+1),
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestEnvHosts_KeepsOnlyHostCandidatesAndNothingFromSecrets(t *testing.T) {
	var hosts EnvHosts
	if err := json.Unmarshal(seededEnvironment(t), &hosts); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, host := range hosts.Hosts() {
		names = append(names, fmt.Sprintf("%s/%d", host.Name, host.Port))
	}
	// Le mot de passe semé passe l'extraction (c'est un mot) : seule la
	// résolution contre l'inventaire de la machine le jette (dockerwatch).
	// Ce qui ne doit jamais rester : l'info d'utilisateur, le jeton de la requête.
	for _, want := range []string{"db/5432", "cache/6379", "cache/0", "pg.internal/5433"} {
		if !slices.Contains(names, want) {
			t.Errorf("host %q missing from %v", want, names)
		}
	}
	joined := strings.Join(names, " ")
	for _, secret := range []string{seededUserinfo, "p4ss-Xk29", "svc-user", seededQuery, "sslmode", "/usr/"} {
		if strings.Contains(joined, secret) {
			t.Errorf("%q leaked into the candidates: %v", secret, names)
		}
	}
}

func TestEnvHosts_NeverLeavesThroughAnyOutput(t *testing.T) {
	var hosts EnvHosts
	if err := json.Unmarshal(seededEnvironment(t), &hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(hosts); err == nil {
		t.Error("EnvHosts must refuse to be serialised")
	}
	if _, err := json.Marshal(struct{ Env EnvHosts }{hosts}); err == nil {
		t.Error("EnvHosts must refuse to be serialised inside another value")
	}
	var logged bytes.Buffer
	slog.New(slog.NewTextHandler(&logged, nil)).Info("environment", "hosts", hosts)
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("environment", "hosts", hosts)
	rendered := fmt.Sprintf("%v|%+v|%#v|%s", hosts, hosts, hosts, hosts) + logged.String()
	for _, secret := range []string{seededPassword, seededToken, "p4ss-Xk29", "svc-user", "DB_PASSWORD", "API_TOKEN", "db:5432", "cache"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("%q leaked through a printer: %s", secret, rendered)
		}
	}
}

func TestEnvHosts_BadShapesAreRefusedWithoutEchoingTheValue(t *testing.T) {
	cases := map[string]string{
		"not a list":    `"DB_PASSWORD=` + seededPassword + `"`,
		"not a string":  `[{"DB_PASSWORD":"` + seededPassword + `"}]`,
		"unterminated":  `["DB_PASSWORD=` + seededPassword,
		"number inside": `[1234]`,
	}
	for name, body := range cases {
		var hosts EnvHosts
		err := json.Unmarshal([]byte(body), &hosts)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), seededPassword) {
			t.Errorf("%s: the error echoes the value: %v", name, err)
		}
	}
	var none EnvHosts
	if err := json.Unmarshal([]byte("null"), &none); err != nil || len(none.Hosts()) != 0 {
		t.Errorf("a container without environment: %v %v", none, err)
	}
}

func TestEnvHosts_IsBoundedAndDeduplicated(t *testing.T) {
	entries := make([]string, 0, 3*MaxEnvCandidates)
	for index := range 3 * MaxEnvCandidates {
		entries = append(entries, fmt.Sprintf("HOST_%d=host%d", index, index))
	}
	entries = append(entries, "AGAIN=host0")
	encoded, _ := json.Marshal(entries)
	var hosts EnvHosts
	if err := json.Unmarshal(encoded, &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts.Hosts()) != MaxEnvCandidates {
		t.Errorf("%d candidates, want the bound %d", len(hosts.Hosts()), MaxEnvCandidates)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]EnvHost{
		"db":                             {Name: "db"},
		"DB":                             {Name: "db"},
		"db:5432":                        {Name: "db", Port: 5432},
		"10.0.0.7:80":                    {Name: "10.0.0.7", Port: 80},
		"redis://cache:6379":             {Name: "cache", Port: 6379},
		"redis://:secret@cache:6379/1":   {Name: "cache", Port: 6379},
		"http://u:p@api/path?x=1#frag":   {Name: "api"},
		"postgres://u:p@a@db/x":          {Name: "db"},
		"amqp://guest:guest@rabbit:5672": {Name: "rabbit", Port: 5672},
	}
	for value, want := range cases {
		got, ok := hostOf(value)
		if !ok || got != want {
			t.Errorf("hostOf(%q) = %+v %v, want %+v", value, got, ok, want)
		}
	}
	for _, value := range []string{"", "a b", "/var/lib", "db:0", "db:70000", "db:80:90", "db:08", "ftp://", "://x", "x y://z", "a/b", "[::1]:80", "user@db", "pass word", "=", "db:port"} {
		if got, ok := hostOf(value); ok {
			t.Errorf("hostOf(%q) = %+v, want nothing", value, got)
		}
	}
}

func TestInspectEnvironment_ReadsHostsOnly(t *testing.T) {
	client := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Id":"abc","Config":{"Image":"x","Env":` + string(seededEnvironment(t)) + `}}`))
	}))
	hosts, err := client.InspectEnvironment(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts.Hosts()) == 0 {
		t.Fatal("no host read")
	}
}
