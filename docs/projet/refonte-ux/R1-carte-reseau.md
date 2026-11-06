# R1 : la carte Réseau en arbre (#457)

Maquette : `docs/projet/maquettes/reseau-carte.html` (trois variantes : complet, sans Proxmox ni pare-feu, quarante machines).
Fonctionnement : `docs/projet/22-fonctionnement.md`, « La carte Réseau de la vue d'ensemble ».

> **Remise en page #507** : la maquette et les captures `R1-maquette-*` sont celles
> de la première version (hôte qui porte ses invités, colonne d'interfaces). Les
> captures `R1-carte-*` sont celles de la mise en page actuelle : trois régions,
> couloirs de zone, cartes de machine.

| Cas | Capture (clair / sombre) |
| --- | --- |
| Maquette, tout configuré | `captures/R1-maquette-A-clair.png`, `R1-maquette-A-sombre.png` |
| Carte réelle du serveur de données fictives | `captures/R1-carte-reelle-{clair,sombre}.png` |
| Forme d'un vrai réseau à 1900 px : VPS de type inconnu, tunnel, pare-feu à treize interfaces, hôte et dix invités sur cinq zones, Pi sans service, deux entrées publiques | `captures/R1-carte-complete-{clair,sombre}.png`, la page entière : `R1-carte-complete-page-clair.png` |
| La même à 1280 px | `captures/R1-carte-complete-1280-{clair,sombre}.png` |
| Survol de l'hôte (invités en évidence), zones sans machine dépliées | `captures/R1-carte-survol-hote-{clair,sombre}.png` |
| Preuve d'un lien au survol (info-bulle du tunnel) | `captures/R1-carte-survol-lien-{clair,sombre}.png` |
| Liste des liens ouverte | `captures/R1-carte-liste-liens-{clair,sombre}.png` |
| Sans Proxmox, sans pare-feu, sans VPS | `captures/R1-carte-a-plat-{clair,sombre}.png` |
| Quarante machines, repliée ; un hôte déplié | `captures/R1-carte-quarante-{repliee,ouverte}-{clair,sombre}.png` |
| 400 px | `captures/R1-carte-mobile-clair.png` |

## Choix et coûts

| Choix | Ce que ça rapporte | Ce que ça coûte |
| --- | --- | --- |
| La déduction vit côté serveur (`internal/netmap`, fonction pure, testée) | Le front ne devine rien ; trois cas testés en Go | Une réponse plus lourde ; la carte relit tout à chaque ouverture |
| Tag VLAN lu par le relevé Proxmox existant, **gardé en mémoire**, pas en base | Aucune migration | Après un redémarrage, la zone d'un invité sans adresse manque jusqu'au premier relevé |
| Boîtes HTML + calque SVG mesuré | Texte, focus et responsive natifs | Un trait peut en croiser un autre ; sous 860 px les traits sont cachés, la liste les dit |
| Teintes de zone dérivées de `--accent` (`oklch` relatif) | Aucun jeton ajouté, suit le thème | Navigateurs sans syntaxe de couleur relative : zones sans teinte (le nom reste écrit) |
| Tunnel reconnu aux noms d'appareils (`wg…`, `tun…`, `ovpn…`, `ipsec…`, `gif…`) et de service (« wireguard ») | Aucune configuration | Un tunnel sous un autre nom n'est pas dessiné |

## Défauts du même genre ailleurs

- La carte d'une **machine** (onglet Réseau) garde l'ancien graphe de conteneurs : hors périmètre, à reprendre si Lucas veut la même présentation.
- Les cartes de **fiche pare-feu** (`FirewallInterfaces`) listent déjà les zones ; leur teinte n'est pas celle de la carte : à aligner plus tard.
- Aucune lecture neuve, aucun droit d'agent, aucune migration : pas de banc de non-fuite à ajouter ; le banc du relevé Proxmox garde seulement MAC, pont et tag (`TestNICOf_KeepsNothingElse`, valeur secrète semée).
