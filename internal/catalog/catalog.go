package catalog

import "time"

// Diagnostiquer lit la machine et n'écrit rien : cinq minutes suffisent, et
// RuntimeMaxSec la tue passé ce délai même si openCloud est éteint.
const diagnostiquerTimeout = 5 * time.Minute

// Enrôler crée un compte, pose trois fichiers et recharge sshd : rien de long,
// et un rechargement qui n'aboutit pas ne doit pas tenir la machine.
const enrolerTimeout = 5 * time.Minute

// Installer le proxy tire une image de quelques dizaines de mébioctets, puis
// attend que Traefik réponde : dix minutes couvrent un lien lent sans laisser
// la machine tenue si le proxy ne démarre jamais.
const proxyTimeout = 10 * time.Minute

// Poser le socle télécharge et déballe Docker : sur un lien lent, ou derrière
// un miroir qui rame, un quart d'heure n'est pas de trop.
const socleTimeout = 15 * time.Minute

// Les deux actions d'hôte virtuel lisent la machine, posent ou retirent un
// fichier, puis attendent que le proxy relise son dossier : quinze essais à
// une seconde, et rien de long avant. Cinq minutes couvrent tout.
const vhostTimeout = 5 * time.Minute

// Poser le jeton DNS compare un fichier, le pose, puis redémarre le proxy et
// attend qu'il réponde : quinze essais à une seconde, plus le temps que
// compose arrête et relance le conteneur. Cinq minutes couvrent tout.
const dnsTokenTimeout = 5 * time.Minute

// Definitions rend le catalogue dans un ordre stable. Chaque appel construit
// sa tranche : rien de partagé, donc rien de modifiable par un appelant.
func Definitions() []Definition {
	return []Definition{
		{
			Kind:       KindDiagnostiquer,
			Label:      "Diagnostiquer",
			Summary:    "Lit l'état de la machine — système, horloge, espace, unités, ports, lanceur — sans rien y changer.",
			Scope:      ScopeMachine,
			Place:      PlaceTarget,
			Reversible: true,
			Interrupts: false,
			Timeout:    diagnostiquerTimeout,
		},
		{
			Kind:  KindDNSToken,
			Label: "Poser le jeton DNS",
			Summary: "Pose sur la machine le jeton Cloudflare de la zone, à côté des certificats. " +
				"Le proxy redémarre si le fichier change : c'est au démarrage que le résolveur le lit.",
			Scope:      ScopeDomain,
			Place:      PlaceTarget,
			Reversible: true,
			// Traefik redémarre quand le jeton change : quelques secondes sans
			// réponse sur la machine, et l'écran « avant » le dit.
			Interrupts: true,
			Timeout:    dnsTokenTimeout,
			Params: []ParamSpec{
				{Name: paramZone, Label: "zone Cloudflare", Type: ParamDomain, Required: true},
			},
		},
		{
			Kind:  KindEnroler,
			Label: "Enrôler",
			Summary: "Crée le compte de service, sa règle sudo et son drop-in sshd, pose la clé d'openCloud " +
				"et affiche l'empreinte d'hôte à saisir. Jouée par l'opérateur sur la machine, en root.",
			Scope:      ScopeMachine,
			Place:      PlaceTarget,
			Reversible: true,
			Interrupts: false,
			Timeout:    enrolerTimeout,
			Params: []ParamSpec{
				{
					Name:     "public_key",
					Label:    "clé publique d'openCloud pour cette machine",
					Type:     ParamPublicKey,
					Required: true,
				},
			},
		},
		{
			Kind:       KindProxy,
			Label:      "Installer le proxy",
			Summary:    "Pose Traefik sur la machine, avec sa configuration statique et son résolveur DNS-01.",
			Scope:      ScopeMachine,
			Place:      PlaceTarget,
			Reversible: true,
			Interrupts: false,
			Timeout:    proxyTimeout,
		},
		{
			Kind:       KindSocle,
			Label:      "Poser le socle",
			Summary:    "Installe Docker et les outils de base, crée /srv.",
			Scope:      ScopeMachine,
			Place:      PlaceTarget,
			Reversible: true,
			Interrupts: false,
			Timeout:    socleTimeout,
		},
		{
			Kind:  KindVhost,
			Label: "Créer un hôte virtuel",
			Summary: "Publie un nom sur le proxy de la machine, vers le conteneur du service. " +
				"Le port n'est pas saisi : il est lu dans la définition du service.",
			Scope:      ScopeDomain,
			Place:      PlaceTarget,
			Reversible: true,
			Interrupts: false,
			Timeout:    vhostTimeout,
			Params: []ParamSpec{
				{Name: paramDomain, Label: "nom de domaine", Type: ParamDomain, Required: true},
				{Name: paramEnvironment, Label: "environnement", Type: ParamSlug, Required: true},
				{Name: paramService, Label: "service", Type: ParamSlug, Required: true},
			},
		},
		{
			Kind:       KindVhostRemove,
			Label:      "Supprimer un hôte virtuel",
			Summary:    "Retire le fragment du nom : le proxy cesse de le servir, et il répond 404.",
			Scope:      ScopeDomain,
			Place:      PlaceTarget,
			Reversible: true,
			Interrupts: true,
			Timeout:    vhostTimeout,
			Params: []ParamSpec{
				{Name: paramDomain, Label: "nom de domaine", Type: ParamDomain, Required: true},
			},
		},
	}
}

// Lookup rend la définition d'une action, et dit si elle existe.
func Lookup(kind Kind) (Definition, bool) {
	for _, definition := range Definitions() {
		if definition.Kind == kind {
			return definition, true
		}
	}
	return Definition{}, false
}
