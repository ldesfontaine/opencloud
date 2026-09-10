package catalog

import (
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/scripts"
	"github.com/ldesfontaine/opencloud/internal/validate"
)

// filesOfProxy rend les fichiers de l'action, par nom.
func filesOfProxy(t *testing.T) map[string]string {
	t.Helper()

	prepared, err := Prepare(KindProxy, nil)
	if err != nil {
		t.Fatalf("préparer Installer le proxy : %v", err)
	}
	byName := map[string]string{}
	for _, file := range prepared.Files {
		byName[file.Path] = string(file.Content)
	}
	return byName
}

// Les cinq fichiers que l'action dépose : la ligne d'image que le script lit,
// et les quatre fichiers qu'il pose.
func TestPrepareProxy_DepositsTheFilesTheScriptPoses(t *testing.T) {
	files := filesOfProxy(t)

	expected := []string{
		proxyImageName,
		proxyCommonMakefileName,
		proxyStaticConfigName,
		proxyComposeName,
		proxyMakefileName,
	}
	if len(files) != len(expected) {
		t.Fatalf("l'action dépose %d fichiers, attendu %d : %v", len(files), len(expected), files)
	}
	for _, name := range expected {
		if strings.TrimSpace(files[name]) == "" {
			t.Errorf("le fichier %q est absent ou vide", name)
		}
	}
}

// L'image est épinglée par digest, et le tag lisible dit la même version que
// la constante : les deux se remplacent ensemble ou pas du tout.
func TestProxyImage_IsPinnedByADigestAndCarriesItsVersion(t *testing.T) {
	if err := validate.Digest(traefikDigest); err != nil {
		t.Fatalf("le digest épinglé n'en est pas un : %v", err)
	}
	if !strings.HasPrefix(traefikVersion, "v3.") {
		t.Errorf("version = %q : le catalogue épingle une Traefik v3", traefikVersion)
	}

	image := proxyImage()
	if !strings.Contains(image, ":"+traefikVersion+"@") {
		t.Errorf("image = %q, sans le tag %q", image, traefikVersion)
	}
	if !strings.HasSuffix(image, "@"+traefikDigest) {
		t.Errorf("image = %q, sans le digest épinglé", image)
	}

	// C'est cette ligne que le script lit pour tirer l'image avant qu'un
	// fichier posé la nomme.
	files := filesOfProxy(t)
	if files[proxyImageName] != image+"\n" {
		t.Errorf("files/%s = %q, attendu %q", proxyImageName, files[proxyImageName], image+"\n")
	}
	if !strings.Contains(files[proxyComposeName], "image: "+image) {
		t.Errorf("le compose.yaml ne nomme pas l'image épinglée :\n%s", files[proxyComposeName])
	}
}

// Le script pose ces fichiers à ces chemins-là : Go et le shell disent les
// mêmes, sinon la pose écrirait ailleurs que ce que l'écran a montré.
func TestProxyPaths_AreTheOnesTheScriptWrites(t *testing.T) {
	script, err := scripts.Script(string(KindProxy))
	if err != nil {
		t.Fatalf("assembler le script : %v", err)
	}
	assembled := string(script)

	for _, path := range []string{
		proxyServiceDir,
		proxyCommonMakefile,
		proxyFragmentsDir,
		proxyAcmeDir,
		proxyContainerName,
		proxyTokenFileName,
	} {
		if !strings.Contains(assembled, path) {
			t.Errorf("le script ne nomme pas %q", path)
		}
	}
}

// Ce qui protège la machine derrière le proxy. Chaque ligne est là pour une
// raison : la perdre en silence est exactement ce que ce test empêche.
func TestProxyCompose_HoldsTheHardeningAndNoDockerSocket(t *testing.T) {
	compose := filesOfProxy(t)[proxyComposeName]

	for _, expected := range []string{
		"read_only: true",
		"- ALL",
		"- NET_BIND_SERVICE",
		"- no-new-privileges:true",
		"restart: unless-stopped",
		`"80:80"`,
		`"443:443"`,
		"CF_DNS_API_TOKEN_FILE: " + proxyContainerTokenFile,
		proxyServiceDir + "/" + proxyStaticConfigName + ":" + proxyContainerStaticConfigFile + ":ro",
		proxyFragmentsDir + ":" + proxyContainerFragmentsDir + ":ro",
		proxyAcmeDir + ":" + proxyContainerAcmeDir,
		"max-size:",
	} {
		if !strings.Contains(compose, expected) {
			t.Errorf("le compose.yaml ne porte pas %q", expected)
		}
	}

	// Le fournisseur file suffit : une socket Docker montée donnerait la
	// machine entière au proxy.
	if strings.Contains(compose, "docker.sock") {
		t.Errorf("le compose.yaml monte la socket Docker :\n%s", compose)
	}
}

// La configuration statique : le clair redirige en 301, les fragments sont
// relus tout seuls, le résolveur DNS-01 est déclaré, et rien n'est exposé.
func TestProxyStaticConfig_RedirectsAndDeclaresTheDnsResolver(t *testing.T) {
	config := filesOfProxy(t)[proxyStaticConfigName]

	for _, expected := range []string{
		`address: ":80"`,
		`address: ":443"`,
		"to: websecure",
		"scheme: https",
		"permanent: true",
		"directory: " + proxyContainerFragmentsDir,
		"watch: true",
		"storage: " + proxyContainerAcmeStorage,
		"provider: cloudflare",
		"dashboard: false",
		"insecure: false",
		"accessLog:",
	} {
		if !strings.Contains(config, expected) {
			t.Errorf("la configuration statique ne porte pas %q", expected)
		}
	}
}

// Le proxy est un service sous la norme : son Makefile inclut les cibles
// communes, et les cibles communes sont celles que 03-modele.md nomme.
func TestProxyMakefile_IncludesEveryStandardTarget(t *testing.T) {
	files := filesOfProxy(t)

	if !strings.Contains(files[proxyMakefileName], "include "+proxyCommonMakefile) {
		t.Errorf("le Makefile du proxy n'inclut pas %s :\n%s", proxyCommonMakefile, files[proxyMakefileName])
	}

	common := files[proxyCommonMakefileName]
	for _, target := range []string{
		"up", "down", "restart", "status", "logs", "config",
		"pull", "update", "build", "shell", "backup", "restore", "clean",
	} {
		if !strings.Contains(common, "\n"+target+":\n") {
			t.Errorf("Makefile.common ne définit pas la cible « %s »", target)
		}
	}
	// openCloud appelle « config » avant « up » : elle ne doit rien écrire.
	if !strings.Contains(common, "config:\n\t$(COMPOSE) config\n") {
		t.Error("la cible config doit se borner à « compose config »")
	}
}
