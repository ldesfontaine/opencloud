package registry

import (
	"path/filepath"
	"testing"
)

// Le trousseau de Docker est celui du compte qui fait tourner l'agent : son
// dossier personnel, pas celui de root par défaut.
func TestConfigPath_FollowsTheHomeOfTheAccount(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "")
	if got, want := ConfigPath("/home/opencloud"), filepath.Join("/home/opencloud", ".docker", "config.json"); got != want {
		t.Errorf("a home: %q, want %q", got, want)
	}
	if got, want := ConfigPath("/root"), filepath.Join("/root", ".docker", "config.json"); got != want {
		t.Errorf("root: %q, want %q", got, want)
	}
	// Sans dossier personnel connu : pas de trousseau, jamais celui d'un autre.
	if got := ConfigPath(""); got != "" {
		t.Errorf("no home: %q", got)
	}
}

func TestConfigPath_DockerConfigWinsOverTheHome(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "/etc/opencloud/docker")
	if got, want := ConfigPath("/home/opencloud"), "/etc/opencloud/docker/config.json"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := ConfigPath(""), "/etc/opencloud/docker/config.json"; got != want {
		t.Errorf("without a home: got %q, want %q", got, want)
	}
}

// Un chemin vide ne lit rien et ne fait pas d'erreur : le registre est joint
// sans identifiants.
func TestFile_WithoutAPathHasNoCredentials(t *testing.T) {
	if _, found := (File{}).Lookup("registry.example.org"); found {
		t.Fatal("an empty path found credentials")
	}
}
