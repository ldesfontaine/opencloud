package catalog

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/scripts"
)

// Les paramètres des deux actions d'hôte virtuel. Le port n'en est pas un : il
// est lu sur la machine, dans la définition du service (15-catalogue-actions.md
// §3).
const (
	paramDomain      = "domain"
	paramEnvironment = "environment"
	paramService     = "service"
)

// Le fragment déposé sous files/, et le fichier qu'il devient sur la machine.
const (
	vhostFragmentName   = "fragment.yml"
	vhostFragmentSuffix = ".yml"
	// La borne d'un nom de fichier sur ext4, xfs et btrfs. Un nom de domaine
	// va jusqu'à 253 octets, le suffixe en ajoute quatre : la fin de la plage
	// ne tient pas (15-catalogue-actions.md §5).
	maxFileNameBytes = 255
)

// vhostPortPlaceholder : le seul trou du fragment rendu par Go. openCloud ne
// connaît pas le port du service — il vit dans la définition posée sur la
// machine —, et le script le remplace par ce qu'il y lit, après l'avoir borné.
// Un trou nommé vaut mieux qu'un fichier composé sur la machine.
const vhostPortPlaceholder = "@OC_PORT@"

// VhostContainerName : le nom du conteneur que le proxy joint sur le réseau
// partagé. Il est dérivé de l'environnement et du service, jamais saisi — le
// contrat d'un service publié l'impose (03-modele.md).
//
// C'est un nom de conteneur, pas un nom de service compose : Docker résout un
// nom de conteneur sur tout réseau partagé et le garantit unique sur la
// machine, alors qu'un nom de service n'est qu'un alias de son projet — deux
// projets peuvent porter le même « web » sur le réseau partagé.
func VhostContainerName(environment, service string) string {
	return environment + "-" + service
}

// VhostFragmentPath : le fichier que le proxy relit tout seul, un par domaine.
func VhostFragmentPath(domain string) string {
	return proxyFragmentsDir + "/" + domain + vhostFragmentSuffix
}

// vhostValues est ce que le gabarit du fragment connaît. Le domaine est validé
// avant d'arriver ici : ni guillemet, ni antislash, ni accent grave.
type vhostValues struct {
	Domain    string
	Container string
	Port      string
	Network   string
}

// vhostFiles rend le fragment de l'hôte virtuel. Le script le pose après y
// avoir remplacé le port, et rien d'autre.
func vhostFiles(params map[string]string) ([]File, error) {
	domain := params[paramDomain]
	if err := refuseTooLongForAFileName(domain); err != nil {
		return nil, err
	}

	values := vhostValues{
		Domain:    domain,
		Container: VhostContainerName(params[paramEnvironment], params[paramService]),
		Port:      vhostPortPlaceholder,
		Network:   SharedNetwork,
	}
	content, err := renderVhostFragment(values)
	if err != nil {
		return nil, err
	}
	return []File{{Path: vhostFragmentName, Content: content, Mode: proxyFileMode}}, nil
}

// Le refus se dit en octets, parce que c'est ce que le système compte
// (15-catalogue-actions.md §5).
func refuseTooLongForAFileName(domain string) error {
	needed := len(domain) + len(vhostFragmentSuffix)
	if needed <= maxFileNameBytes {
		return nil
	}
	return refusal.Refusal{
		Cause: fmt.Sprintf("le nom fait %d octets et son fragment en demanderait %d, plus que les %d d'un nom de fichier",
			len(domain), needed, maxFileNameBytes),
		Remedy: "publier un nom plus court",
	}
}

func renderVhostFragment(values vhostValues) ([]byte, error) {
	source, err := scripts.Template(string(KindVhost), vhostFragmentName)
	if err != nil {
		return nil, err
	}
	// missingkey=error : un champ mal orthographié est une faute de code,
	// jamais un fragment posé à trous.
	parsed, err := template.New(vhostFragmentName).Option("missingkey=error").Parse(string(source))
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", vhostFragmentName, err)
	}

	var written bytes.Buffer
	if err := parsed.Execute(&written, values); err != nil {
		return nil, fmt.Errorf("render template %s: %w", vhostFragmentName, err)
	}
	return written.Bytes(), nil
}
