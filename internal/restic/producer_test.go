package restic

import (
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

func TestVolumeProducer_CopiesTheMountAsATarOnStdout(t *testing.T) {
	producer, err := VolumeProducer("/usr/bin/docker", "nextcloud-app-1", "/var/www/html")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/docker", "cp", "nextcloud-app-1:/var/www/html/.", "-"}
	if !slices.Equal(producer, want) {
		t.Fatalf("producer %q", producer)
	}
	root, err := VolumeProducer("/usr/bin/docker", "c", "/")
	if err != nil || root[2] != "c:/." {
		t.Fatalf("root mount %q %v", root, err)
	}
	relay := Repository{Path: agentwire.ResticPathRelay, ProxyURL: "rest:http://127.0.0.1:1/x/", PasswordFile: "/p"}
	command, err := BackupStream("/usr/bin/restic", relay, VolumeFileName("nextcloud_data"), producer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(command.Args, " "), "--stdin-filename volume-nextcloud_data.tar -- /usr/bin/docker cp") {
		t.Fatalf("args %v", command.Args)
	}
}

func TestDatabaseProducer_RunsTheFixedScriptOfTheEngine(t *testing.T) {
	for _, engine := range agentwire.DatabaseEngines() {
		producer, err := DatabaseProducer("/usr/bin/docker", "db-1", engine)
		if err != nil {
			t.Fatalf("%s: %v", engine, err)
		}
		want := []string{"/usr/bin/docker", "exec", "db-1", "sh", "-c", DumpScript(engine)}
		if !slices.Equal(producer, want) || !strings.Contains(DumpScript(engine), DumpFailedMarker) {
			t.Fatalf("%s: producer %q", engine, producer)
		}
		// Aucun mot de passe n'est passé en argument d'un outil : seules les
		// variables du conteneur sont nommées, jamais développées par l'agent.
		if strings.Contains(DumpScript(engine), "--password") || strings.Contains(DumpScript(engine), " -p") {
			t.Errorf("%s: a password flag in the script", engine)
		}
		if strings.Contains(DumpScript(engine), "\n") {
			t.Errorf("%s: the script spans lines", engine)
		}
	}
	if DatabaseFileName("db-1", agentwire.EnginePostgres) != "database-db-1.sql" || DatabaseFileName("m", agentwire.EngineMongo) != "database-m.archive" {
		t.Error("database file names")
	}
}

func TestProducers_RefuseWhatWouldEscape(t *testing.T) {
	for _, container := range []string{"", "-x", "../x", "a/b", "a:b", "a b"} {
		if _, err := VolumeProducer("/usr/bin/docker", container, "/data"); err == nil {
			t.Errorf("volume container %q", container)
		}
		if _, err := DatabaseProducer("/usr/bin/docker", container, agentwire.EnginePostgres); err == nil {
			t.Errorf("database container %q", container)
		}
	}
	for _, destination := range []string{"", "data", "/data/../etc", "/data/", "/a\x00b"} {
		if _, err := VolumeProducer("/usr/bin/docker", "c", destination); err == nil {
			t.Errorf("destination %q", destination)
		}
	}
	if _, err := VolumeProducer("docker", "c", "/data"); err == nil {
		t.Error("relative docker")
	}
	if _, err := DatabaseProducer("/usr/bin/docker", "c", "redis"); err == nil {
		t.Error("unsupported engine")
	}
	// Le plus long nom de conteneur tient dans un nom de fichier de restic.
	relay := Repository{Path: agentwire.ResticPathRelay, ProxyURL: "rest:http://127.0.0.1:1/x/", PasswordFile: "/p"}
	longest := DatabaseFileName(strings.Repeat("a", 128), agentwire.EngineMongo)
	if _, err := BackupStream("/usr/bin/restic", relay, longest, []string{"/usr/bin/docker"}); err != nil {
		t.Errorf("the longest name %d bytes: %v", len(longest), err)
	}
}
