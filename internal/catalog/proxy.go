package catalog

import (
	"bytes"
	"fmt"
	"io/fs"
	"strconv"
	"text/template"

	"github.com/ldesfontaine/opencloud/internal/scripts"
)

// La dernière Traefik v3 stable, épinglée par son digest multi-arch. Le tag
// reste écrit à côté pour qu'un humain sache ce qu'il lit ; c'est le digest
// qui décide.
//
// Pour la renouveler : « docker buildx imagetools inspect traefik:v3.<n> »
// (ou « docker manifest inspect ») donne le digest de l'index multi-arch ;
// remplacer les deux constantes ensemble, jamais l'une sans l'autre.
const (
	traefikVersion = "v3.7.13"
	traefikDigest  = "sha256:f86a2cab1b5c649070c49f883c743dd32d8485a56e3368c5f93b9e91f1e91259"
)

// Ce que le proxy occupe sur la machine, sous la norme /srv
// (04-implantation.md).
const (
	// Le proxy est partagé par les environnements de la machine (03-modele.md)
	// : il ne vit dans aucun d'eux, mais il reste un service sous la norme,
	// avec son compose.yaml et son Makefile.
	proxyServiceDir = "/srv/workspace/system/traefik"
	// Les cibles standard qu'un Makefile de service inclut, à la racine de
	// workspace : le proxy les pose, tout service déployé ensuite les réutilise.
	proxyCommonMakefile = "/srv/workspace/Makefile.common"
	// Les fragments d'hôtes virtuels, déposés un par un par « Créer un hôte
	// virtuel » (15-catalogue-actions.md §2). Vide tant qu'aucun domaine n'est
	// publié.
	proxyFragmentsDir = "/srv/data/traefik"
	// Les certificats, leurs clés privées et le jeton DNS de la zone : ce que
	// Traefik utilise pour obtenir et garder ses certificats, et la seule
	// chose du proxy qui doit survivre (04-implantation.md, 07-donnees…).
	proxyAcmeDir = "/srv/data/acme"
)

// Ce que le proxy voit de l'intérieur du conteneur. Rien ici n'est un chemin
// de la machine : ce qui est monté dessous est dans compose.yaml.
const (
	proxyContainerName             = "traefik"
	proxyClearPort                 = 80
	proxySecurePort                = 443
	proxyContainerStaticConfigFile = "/etc/traefik/traefik.yml"
	proxyContainerFragmentsDir     = "/etc/traefik/dynamic"
	proxyContainerAcmeDir          = "/acme"
	proxyContainerAcmeStorage      = proxyContainerAcmeDir + "/acme.json"
	proxyContainerTokenFile        = proxyContainerAcmeDir + "/" + proxyTokenFileName
)

// proxyTokenFileName : le jeton DNS de la zone, posé par « Faire tourner un
// jeton DNS » à côté des certificats qu'il sert à obtenir. Le script le nomme
// aussi, pour dire à l'opérateur qu'il manque encore.
const proxyTokenFileName = "cloudflare.token"

// Les noms des fichiers déposés sous files/, dans l'ordre où le script s'en
// sert. « image » n'est pas posé sur la machine : c'est la ligne que le script
// lit pour tirer l'image avant qu'un fichier posé la nomme
// (15-catalogue-actions.md §3).
const (
	proxyImageName          = "image"
	proxyCommonMakefileName = "Makefile.common"
	proxyStaticConfigName   = "traefik.yml"
	proxyComposeName        = "compose.yaml"
	proxyMakefileName       = "Makefile"
)

// proxyFileMode : ces fichiers ne portent aucun secret et se lisent à la main.
const proxyFileMode fs.FileMode = 0o644

// proxyValues est ce que les gabarits du proxy connaissent. Aucune valeur ne
// vient de l'opérateur : l'action n'a pas de paramètre.
type proxyValues struct {
	Image         string
	Version       string
	ContainerName string
	ClearPort     string
	SecurePort    string

	// Sur la machine.
	ServiceDir       string
	StaticConfigFile string
	CommonMakefile   string
	FragmentsDir     string
	AcmeDir          string

	// Dans le conteneur.
	ContainerStaticConfigFile string
	ContainerFragmentsDir     string
	ContainerAcmeDir          string
	ContainerAcmeStorage      string
	ContainerTokenFile        string
}

// proxyImage : le tag lisible et le digest qui décide, dans une seule
// référence. Docker tire par le digest et ignore le tag.
func proxyImage() string {
	return proxyContainerName + ":" + traefikVersion + "@" + traefikDigest
}

// ProxyLayout : ce que « Installer le proxy » occupe sur la machine.
// L'interface le montre avant de lancer ; elle ne le recopie pas, sinon elle
// mentirait au premier chemin déplacé.
type ProxyLayout struct {
	Version        string
	Image          string
	ServiceDir     string
	CommonMakefile string
	FragmentsDir   string
	AcmeDir        string
}

// Proxy rend ce que l'action pose, pour qui doit le dire à l'opérateur.
func Proxy() ProxyLayout {
	return ProxyLayout{
		Version:        traefikVersion,
		Image:          proxyImage(),
		ServiceDir:     proxyServiceDir,
		CommonMakefile: proxyCommonMakefile,
		FragmentsDir:   proxyFragmentsDir,
		AcmeDir:        proxyAcmeDir,
	}
}

func newProxyValues() proxyValues {
	return proxyValues{
		Image:         proxyImage(),
		Version:       traefikVersion,
		ContainerName: proxyContainerName,
		ClearPort:     strconv.Itoa(proxyClearPort),
		SecurePort:    strconv.Itoa(proxySecurePort),

		ServiceDir:       proxyServiceDir,
		StaticConfigFile: proxyServiceDir + "/" + proxyStaticConfigName,
		CommonMakefile:   proxyCommonMakefile,
		FragmentsDir:     proxyFragmentsDir,
		AcmeDir:          proxyAcmeDir,

		ContainerStaticConfigFile: proxyContainerStaticConfigFile,
		ContainerFragmentsDir:     proxyContainerFragmentsDir,
		ContainerAcmeDir:          proxyContainerAcmeDir,
		ContainerAcmeStorage:      proxyContainerAcmeStorage,
		ContainerTokenFile:        proxyContainerTokenFile,
	}
}

// proxyFiles rend tout ce que « Installer le proxy » dépose sous files/ : la
// ligne d'image, les cibles standard, la configuration statique, le service et
// son Makefile. Le script les pose, il n'en compose aucun.
func proxyFiles() ([]File, error) {
	values := newProxyValues()

	common, err := scripts.CommonMakefile()
	if err != nil {
		return nil, err
	}
	rendered := []File{
		{Path: proxyImageName, Content: []byte(values.Image + "\n"), Mode: proxyFileMode},
		{Path: proxyCommonMakefileName, Content: common, Mode: proxyFileMode},
	}

	for _, name := range []string{proxyStaticConfigName, proxyComposeName, proxyMakefileName} {
		content, err := renderProxyTemplate(name, values)
		if err != nil {
			return nil, err
		}
		rendered = append(rendered, File{Path: name, Content: content, Mode: proxyFileMode})
	}
	return rendered, nil
}

func renderProxyTemplate(name string, values proxyValues) ([]byte, error) {
	source, err := scripts.Template(string(KindProxy), name)
	if err != nil {
		return nil, err
	}
	// Option missingkey=error : un champ mal orthographié dans un gabarit est
	// une faute de code, jamais un fichier posé à trous.
	parsed, err := template.New(name).Option("missingkey=error").Parse(string(source))
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", name, err)
	}

	var written bytes.Buffer
	if err := parsed.Execute(&written, values); err != nil {
		return nil, fmt.Errorf("render template %s: %w", name, err)
	}
	return written.Bytes(), nil
}
