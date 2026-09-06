# L'architecture du programme — qui fait quoi

Un binaire Go. Chaque composant est un package sous `internal/`, avec une
interface, un constructeur `New(deps)`, aucune variable globale.

## Les composants

| Composant | Rôle | Dépend de |
|---|---|---|
| **`config`** | Charge le TOML, décode en struct typée, valide, **puis** publie. Clé inconnue = avertissement. | — |
| **`store`** | SQLite (`modernc`, sans cgo), migrations embarquées et numérotées. Tables : machines, environnements, services, domaines, certificats, **actions** (le journal de transaction), lots, comptes, notifications. | `config` |
| **`catalog`** | Le catalogue : pour chaque action, nom, portée, schéma de paramètres, script embarqué, délai maximum, réversibilité, interruption. **Valide les paramètres** (`validate`), **rend les fichiers** (gabarits `text/template`). | `scripts`, `validate` |
| **`scripts`** | `embed.FS` : un `run.sh` par action, `Makefile.common`, gabarits Traefik / compose / unités. Vérifiés par `shellcheck` en CI. | — |
| **`validate`** | Les types et bornes du §4 du catalogue : domaine, port, slug, compte, slot, digest, adresse. Un seul endroit. | — |
| **`transport`** | Interface : `Put(fichier, mode)`, `Run(vecteur)`, `Follow(unité) → flux`, `Read(fichier)`. Deux mises en œuvre : **`local`** et **`ssh`** (client borné option par option). | — |
| **`runner`** | **Une file par machine.** Prépare (journal `préparée`, dépôt de `run.sh` + `params.env` + `files/`), lance (`systemd-run`), suit (journald → flux), conclut (`appliquée` / `échouée` / `refusée`). **Reprise au démarrage** de toute action `préparée` ou `en cours`. Décompose une action de portée infrastructure en lot. | `store`, `catalog`, `transport` |
| **`enroll`** | Génère la commande d'enrôlement (compte, clé, sudo, sshd, option NAT WireGuard), tient la paire de clés par machine et le `known_hosts` dédié. | `store`, `validate` |
| **`dns`** | Client Cloudflare : écrire et effacer un TXT `_acme-challenge`, et — sur demande — poser un A/CNAME. Les **deux seules écritures** faites ailleurs que sur une machine. Un jeton par zone, et la rotation d'un jeton. | `config` |
| **`acme`** | Obtenir et renouveler un certificat en DNS-01 ; le déposer sur la machine **ou** le laisser à Traefik — le mécanisme est encore ouvert, le composant l'isole. | `dns`, `transport` |
| **`observe`** | Lit les sources : santé (collecteur), état des unités et conteneurs (action *Diagnostiquer*), certificats (expiration), date de la dernière copie froide. **Chaque donnée porte son âge.** | `store`, `runner` |
| **`notify`** | Notifications : espace libre proche du seuil, élagage déclenché, sauvegarde ou test de restauration échoué, certificat en échec, et **toute tâche périodique qui cesse de se manifester** — la copie froide trop ancienne en est un cas. Journal + interface d'abord ; connecteur plus tard. | `store` |
| **`coldbackup`** | Récupération froide par `rsync` depuis la machine openCloud ; date affichée. | `transport` |
| **`auth`** | Session, identifiant et mot de passe, changement forcé à la première connexion. Modèle prêt pour plusieurs comptes. | `store` |
| **`web`** | Serveur HTTP, `html/template` (échappement automatique), HTMX pour les fragments, **SSE** pour le direct des actions. Handlers minces : valider, appeler un composant, rendre. | tout |
| **`cli`** | `opencloud serve`, `opencloud enroll-command <machine>`, `opencloud status`, `opencloud self-update`, `opencloud version`. Rien de plus. | `config`, `web`, `selfupdate` |

## Ce qui n'est pas dans le binaire

Traefik, CrowdSec, Netdata ou Beszel, Loki et Alloy, Docker : **déployés par
des actions**, jamais embarqués.

## Le flux d'une action

```
web  →  catalog.Validate(params)
     →  store : journal « préparée » (script, empreinte, params, retour arrière)
     →  runner.Enqueue(machine)             une file par machine, en série
     →  transport.Put ×3  (run.sh, params.env, files/)
     →  transport.Run   (sudo -n systemd-run --unit=oc-action-<id> …)
     →  transport.Follow(oc-action-<id>)    journald → SSE → navigateur
     →  store : « appliquée » / « échouée » / « refusée » + constat
```

Une action de portée infrastructure entre dans `runner` comme un **lot** :
N actions, N files, un seul identifiant de lot pour l'affichage.

**Le lot rend, par machine, `réussi` / `échoué` / `non tenté`.** Le troisième
n'est pas un détail d'affichage : c'est ce que `runner` doit savoir distinguer
pour ne rejouer que ce qui n'a pas abouti (`05-execution.md`).

## Les frontières à tenir

- **`web` ne lance rien** : il dépose dans `runner`. Il ne connaît ni SSH ni
  systemd.
- **`runner` ne compose aucune commande** : les vecteurs sont des constantes ;
  seul l'identifiant varie.
- **`catalog` ne touche pas au réseau** : il valide et rend des fichiers.
- **`transport` ne sait pas ce qu'il transporte.**
- **`observe` ne modifie rien** : lecture seule, toujours datée.

## La suite logique du développement

| # | Ce qu'on livre | Composants | Ça marche quand… |
|---|---|---|---|
| 1 | Le squelette | `config`, `store`, `auth`, `web`, `cli` | On se connecte, on change le mot de passe, la page est vide. |
| 1 bis | **Installable** | `make release` (`nfpm`), unité, `config.toml`, `self-update` | `apt install ./opencloud.deb` sur une Debian neuve : l'interface répond ; `apt install` de la version suivante met à jour sans rien perdre (`20-installation-et-mise-a-jour.md`). |
| 2 | **Une action en local, suivie en direct** | `validate`, `scripts`, `catalog`, `transport/local`, `runner` | *Diagnostiquer* s'exécute par `systemd-run` sur la machine de dev, la sortie défile dans le navigateur, le journal passe à `appliquée`. Coupure au milieu → reprise propre. **C'est le cœur ; tout le reste sont des actions.** |
| 3 | Une deuxième machine | `enroll`, `transport/ssh` | La commande générée enrôle une machine ; *Tester l'accès* répond. |
| 4 | Publier | actions *socle*, *proxy*, *hôte virtuel* | Un conteneur témoin répond en HTTP par son nom. |
| 5 | Certifier | `dns`, `acme`, page certificats | Le même nom répond en HTTPS, la date d'expiration s'affiche. |
| 6 | Services | actions *déclarer*, *déployer*, *mettre à jour*, `Makefile.common` | Un service posé par formulaire tourne ; `update` le met à jour. |
| 7 | Sauvegarder | actions *configurer*, *sauvegarder*, *restaurer*, `coldbackup` | Une minuterie tourne, une restauration ramène l'état, la date froide s'affiche. |
| 8 | Observer | `observe`, `notify`, actions *collecteur*, *CrowdSec* | Santé, journaux, alertes. |

Après le point 2, chaque étape est une action de plus et un écran de plus.
