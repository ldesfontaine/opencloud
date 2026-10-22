package agentwire

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func composeUnits() []ComposeUnit {
	return []ComposeUnit{
		{Name: "app", Image: "ghcr.io/example/nextcloud:29.0.4", Ports: []string{"8080:80", "443/tcp"}, DataPath: "/var/www/html"},
		{Name: "db", Image: "postgres:16.4"},
	}
}

// Les valeurs qu'un annuaire corrompu, un fichier d'apply ou une proposition
// pourraient porter : chacune ajouterait une clé, un montage ou une
// interpolation si elle était recopiée dans le texte.
var hostileValues = []string{
	"x\nprivileged: true",
	"x\n    privileged: true",
	"x:\n  network_mode: host",
	"x # commentaire",
	"x: {pid: host}",
	"${DB_PASSWORD}",
	"x$y",
	`x"y`,
	"x'y",
	"x\ty",
	"x y",
	"/:/host",
	"-x",
	"",
}

// Chaque champ, tour à tour, reçoit chaque valeur hostile : le rendu la
// refuse avec le refus de ce champ, jamais en l'écrivant.
func TestRenderCompose_RefusesEachHostileValueFieldByField(t *testing.T) {
	fields := []struct {
		name string
		set  func(project, serviceID *string, units []ComposeUnit, value string)
		want error
	}{
		{"projet", func(p, _ *string, _ []ComposeUnit, v string) { *p = v }, ErrComposeProject},
		{"service", func(_, s *string, _ []ComposeUnit, v string) { *s = v }, ErrComposeServiceID},
		{"nom d'unité", func(_, _ *string, u []ComposeUnit, v string) { u[0].Name = v }, ErrComposeUnitName},
		{"image", func(_, _ *string, u []ComposeUnit, v string) { u[0].Image = v }, ErrComposeImage},
		{"image (registre)", func(_, _ *string, u []ComposeUnit, v string) { u[0].Image = v + ".example/app:1.0" }, ErrComposeImage},
		{"image (version)", func(_, _ *string, u []ComposeUnit, v string) { u[0].Image = "nginx:" + v }, ErrComposeImage},
		{"port", func(_, _ *string, u []ComposeUnit, v string) { u[0].Ports = []string{v} }, ErrComposePort},
		{"dossier de données", func(_, _ *string, u []ComposeUnit, v string) { u[0].DataPath = v }, ErrComposeDataPath},
	}
	for _, field := range fields {
		for _, value := range hostileValues {
			project, serviceID, units := "nextcloud", "svc_1", composeUnits()
			field.set(&project, &serviceID, units, value)
			compose, _, err := RenderCompose(project, serviceID, units)
			// Un dossier de données vide est « aucun » : le seul cas valable.
			if field.name == "dossier de données" && value == "" {
				if err != nil {
					t.Errorf("%s %q: %v", field.name, value, err)
				}
				continue
			}
			if !errors.Is(err, field.want) {
				t.Errorf("%s %q: got %v, want %v\n%s", field.name, value, err, field.want, compose)
			}
		}
	}
	// Le dossier de données ne remonte pas et ne vise pas la racine.
	for _, target := range []string{
		"/", "/..", "/var/../etc", "/var/", "var", "/var/./www", "/a:ro", "/a,b",
		"/data\nprivileged: true", "/data # x", "/data${X}", `/data"`, "/data:/host",
	} {
		units := composeUnits()
		units[0].DataPath = target
		if _, _, err := RenderCompose("nextcloud", "svc_1", units); !errors.Is(err, ErrComposeDataPath) {
			t.Errorf("data path %q: %v", target, err)
		}
	}
	for _, port := range []string{"0:80", "65536:80", "80:0", "1.2.3.4:80:80", "80/sctp"} {
		units := composeUnits()
		units[0].Ports = []string{port}
		if _, _, err := RenderCompose("nextcloud", "svc_1", units); !errors.Is(err, ErrComposePort) {
			t.Errorf("port %q: %v", port, err)
		}
	}
	units := append(composeUnits(), ComposeUnit{Name: "app", Image: "redis:7.4"})
	if _, _, err := RenderCompose("nextcloud", "svc_1", units); !errors.Is(err, ErrComposeUnitName) {
		t.Errorf("two units of the same name: %v", err)
	}
}

// Le fichier rendu ne porte que les clés permises, et le seul montage est
// ./data/<unité>.
func TestRenderCompose_WritesOnlyTheAllowedKeys(t *testing.T) {
	compose, dataDirs, err := RenderCompose("nextcloud", "svc_1", composeUnits())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dataDirs, []string{"app"}) {
		t.Fatalf("data dirs %v", dataDirs)
	}
	var read map[string]any
	if err := yaml.Unmarshal(compose, &read); err != nil {
		t.Fatal(err)
	}
	if keysOf(read) != "name,services" {
		t.Fatalf("top keys %s", keysOf(read))
	}
	allowed := []string{"env_file", "image", "labels", "ports", "restart", "volumes"}
	for name, raw := range read["services"].(map[string]any) {
		service := raw.(map[string]any)
		for key := range service {
			if !slices.Contains(allowed, key) {
				t.Errorf("%s: key %q", name, key)
			}
		}
		if volumes, ok := service["volumes"].([]any); ok {
			if len(volumes) != 1 || !strings.HasPrefix(volumes[0].(string), "./data/"+name+":/") {
				t.Errorf("%s: volumes %v", name, volumes)
			}
		}
	}
}

func keysOf(values map[string]any) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}

// L'agent n'accepte que ce que le serveur rend : une clé, une ancre, un
// commentaire ou un montage de plus, et le fichier est refusé, même signé.
func TestCheckCompose_AcceptsOnlyTheRenderedFile(t *testing.T) {
	rendered, dataDirs, err := RenderCompose("nextcloud", "svc_1", composeUnits())
	if err != nil {
		t.Fatal(err)
	}
	compose := string(rendered)
	if err := CheckCompose(compose, "nextcloud", "svc_1", dataDirs); err != nil {
		t.Fatalf("the rendered file is refused: %v", err)
	}
	afterRestart := func(extra string) string {
		return strings.Replace(compose, "    restart: unless-stopped\n", "    restart: unless-stopped\n"+extra, 1)
	}
	variants := map[string]string{
		"privileged":                afterRestart("    privileged: true\n"),
		"network_mode":              afterRestart("    network_mode: host\n"),
		"pid":                       afterRestart("    pid: host\n"),
		"cap_add":                   afterRestart("    cap_add:\n      - SYS_ADMIN\n"),
		"devices":                   afterRestart("    devices:\n      - /dev/sda:/dev/sda\n"),
		"user":                      afterRestart("    user: root\n"),
		"montage de l'hôte":         strings.Replace(compose, "./data/app:/var/www/html", "/:/host", 1),
		"montage remontant":         strings.Replace(compose, "./data/app:/var/www/html", "./data/app/../../..:/host", 1),
		"montage en option":         strings.Replace(compose, "./data/app:/var/www/html", "./data/app:/var/www/html:rw", 1),
		"second montage":            strings.Replace(compose, "      - ./data/app:/var/www/html\n", "      - ./data/app:/var/www/html\n      - /etc:/etc\n", 1),
		"montage d'une autre unité": strings.Replace(compose, "./data/app:/var/www/html", "./data/db:/var/www/html", 1),
		"socket docker":             strings.Replace(compose, "./data/app:/var/www/html", "/var/run/docker.sock:/var/run/docker.sock", 1),
		"interpolation":             strings.Replace(compose, "postgres:16.4", "postgres:${TAG}", 1),
		"commentaire":               compose + "# un commentaire\n",
		"include":                   "include:\n  - /etc/compose.yml\n" + compose,
		"extension":                 compose + "x-extra: 1\n",
		"réseau":                    compose + "networks:\n  default:\n    external: true\n",
		"ancre":                     strings.Replace(compose, "  db:\n", "  db: &db\n", 1),
		"balise":                    strings.Replace(compose, "    image: postgres:16.4\n", "    image: !!str postgres:16.4\n", 1),
		"autre projet":              strings.Replace(compose, "name: nextcloud", "name: autre", 1),
		"autre service":             strings.Replace(compose, "opencloud.service: svc_1", "opencloud.service: svc_2", 1),
		"restart":                   strings.Replace(compose, "restart: unless-stopped", "restart: always", 1),
		"env_file":                  strings.Replace(compose, "path: .env", "path: /etc/shadow", 1),
		"clé en double":             strings.Replace(compose, "    restart: unless-stopped\n", "    restart: unless-stopped\n    image: evil:1.0\n", 1),
		"guillemets ôtés":           strings.Replace(compose, `"8080:80"`, "8080:80", 1),
		"YAML illisible":            "services: [",
		"vide":                      "",
	}
	for name, variant := range variants {
		if variant == compose {
			t.Fatalf("%s: the variant changes nothing", name)
		}
		if err := CheckCompose(variant, "nextcloud", "svc_1", dataDirs); !errors.Is(err, ErrComposeNotOurs) {
			t.Errorf("%s: accepted (%v)\n%s", name, err, variant)
		}
	}
	// Les dossiers de données que l'ordre annonce sont ceux du fichier.
	for _, dirs := range [][]string{nil, {"db"}, {"app", "db"}} {
		if err := CheckCompose(compose, "nextcloud", "svc_1", dirs); err == nil {
			t.Errorf("data dirs %v accepted", dirs)
		}
	}
	if err := CheckCompose(compose, "autre", "svc_1", dataDirs); err == nil {
		t.Error("another project accepted")
	}
}
