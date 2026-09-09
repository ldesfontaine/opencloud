# Installation et mise à jour

Comment openCloud se pose sur une machine, et comment il se met à jour sans
être réinstallé. Fondé sur ce que font les binaires Go comparables (Caddy,
Headscale, Gitea, Beszel, SysWarden) et sur ce qui a cassé chez les autres.

## Un binaire qui embarque tout

Le front HTML, les fichiers statiques, les migrations SQLite, les scripts
d'actions et les gabarits sont **compilés dans le binaire** (`embed.FS`).
Déployer, c'est poser un fichier. Aucun runtime, aucune dépendance.

## Installer : un paquet `.deb`

```bash
sudo apt install ./opencloud_0.1.0_amd64.deb
```

| Le paquet pose | Où |
|---|---|
| Le binaire | `/opt/opencloud/bin/opencloud` — **le nom ne change jamais** entre versions —, et le lien `/usr/bin/opencloud` pour la ligne de commande |
| La configuration | `/etc/opencloud/config.toml` — *conffile* : dpkg ne l'écrase pas à la mise à jour ; `root:opencloud`, `0640`, aucun secret |
| L'état | `/var/lib/opencloud/` — `StateDirectory=` de l'unité, `opencloud:opencloud`, `0700`, préservé par dpkg |
| L'utilisateur système et l'unité durcie | `opencloud`, sans shell ; `opencloud.service` (`packaging/opencloud.service`) |
| Le lanceur | `/opt/opencloud/bin/oc-launch` — copié en `/usr/local/sbin/oc-launch` par l'amorçage (`15-catalogue-actions.md` §1) |

Puis, une fois, `sudo opencloud enroll-local` : la machine openCloud s'enrôle
sur elle-même par SSH vers `localhost` — compte avec un shell et le groupe
`systemd-journal`, clé, règle `sudo` vers le lanceur, drop-in `sshd` — comme
n'importe quelle machine
(`05-execution.md`). Sans ce geste, l'interface tourne mais aucune action ne
part : la fiche de la machine le dit.

Produit par `nfpm` depuis `make release` (`packaging/nfpm.yaml`), par un build
reproductible : deux builds du même commit donnent les mêmes sommes, et la CI
le vérifie. Un tag `vX.Y.Z` déclenche le workflow `release`, qui publie dans
la release GitHub :

| Fichier | Rôle |
|---|---|
| `opencloud_X.Y.Z_amd64.deb` | le paquet, pour `apt install` |
| `opencloud_X.Y.Z_linux_amd64` | le binaire nu, ce que `self-update` télécharge |
| `SHA256SUMS` | les sommes des deux |
| l'attestation de provenance | signée par Sigstore, gardée par GitHub ; elle prouve que chaque fichier a été construit par ce workflow, sur ce dépôt, pour ce tag |

Avant d'installer à la main, vérifier depuis un poste qui a `gh` :

```bash
gh attestation verify opencloud_0.1.0_amd64.deb --repo ldesfontaine/opencloud
sha256sum --ignore-missing -c SHA256SUMS
```

Pas de `curl | sh`.

**Ce que l'attestation couvre, et ce qu'elle suppose.** Le `.deb` n'est pas
produit par du code du dépôt mais par `nfpm`, que `make release` récupère au
build par `go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0`, dans le job
qui porte `id-token: write` et signe. C'est assumé : la version est épinglée,
et le module est vérifié à la récupération par la base de sommes des modules Go
(`sum.golang.org`) — une version publiée ne peut pas changer sous nos pieds.
L'attestation dit donc vrai sur l'origine du fichier — ce workflow, ce dépôt, ce
tag — sans rien dire de plus sur `nfpm` que sa somme. Le coût du vendoring — un
module de plus à suivre et à mettre à jour — dépasse ce qu'il rapporte tant que
le dépôt est privé et la cadence de publication faible. À revoir au passage en
public.

**Entre l'installation et la première connexion**, le compte est `admin` /
`opencloud` et le premier arrivé le prend. Le paquet écoute donc sur
`127.0.0.1:8080` seulement : ouvrir l'interface depuis la machine (ou par un
tunnel SSH), changer le mot de passe, et **seulement ensuite** élargir
`listen` ou poser le proxy devant.

Un dépôt apt signé viendra si la cadence de publication le justifie ; tant
qu'il n'existe pas, un `.deb` téléchargé et vérifié suffit.

**Pourquoi un paquet et pas un script** : dpkg sait déjà préserver `/etc`,
laisser `/var/lib` en place, créer l'utilisateur, activer l'unité, et **mettre
à jour en place** avec `apt install` d'une version plus récente. Un script
refait tout ça à la main, mal — c'est là que Coolify et Cloudron ont cassé.

## Mettre à jour : deux chemins, un seul mécanisme

| Chemin | Geste |
|---|---|
| Par le paquet | `sudo apt install ./opencloud_0.2.0_amd64.deb` |
| Par le binaire | `sudo opencloud self-update` — ou, plus tard, le bouton « mettre à jour » de l'interface, qui l'appellera |

`self-update` prend `--check` (dire ce qui est disponible sans rien toucher)
et `--version vX.Y.Z` (une release précise ; sinon la dernière). Il dit chaque
étape sur une ligne, et affiche la commande de retour arrière. Un refus —
saut de version, somme fausse, attestation absente ou invalide — se lit tel
quel et sort avec le code `2`. Tant que le dépôt des releases est privé, le
fichier `/etc/opencloud/github-token` — `root:root`, `0600`, posé à la main —
porte un jeton GitHub en lecture, sur sa première ligne. `self-update` tourne
en root et le lit seul ; le service, lui, ne le voit pas. Absent, il n'y a pas
de jeton ; plus ouvert que `0600` ou possédé par un autre que root, le fichier
est refusé sans être lu — le refus donne `chmod 0600` ou `chown root:root`.
`apt purge` l'emporte avec `/etc/opencloud`. Le fichier devient inutile le jour
où le dépôt passe en public.

`self-update` **prend un verrou avant tout** : le fichier `opencloud.lock`, à
côté du binaire, posé avant le premier appel réseau et retiré une fois l'unité
redémarrée. Deux `self-update` lancés en même temps : le second refuse sans
rien télécharger. Un verrou resté après une coupure se retire à la main — le
refus dit lequel — ou part avec la prochaine pose du paquet.

Dans les deux cas, ce qui se passe, **dans cet ordre** :

1. **Télécharger** la release, **vérifier** la somme SHA-256 et l'attestation.
   Refus si l'une manque. L'attestation est vérifiée avec `sigstore-go`, la
   bibliothèque de `gh` : la seule dépendance lourde du binaire, assumée
   plutôt qu'une vérification cryptographique réécrite à la main. Deux
   instances Sigstore peuvent avoir signé — celle de GitHub tant que le dépôt
   est privé, l'instance publique ensuite — et le binaire reconnaît l'une et
   l'autre.
2. **Refuser un saut de version mineure** : `0.1 → 0.3` est refusé, le chemin
   est séquentiel *(Headscale)*. **Refuser aussi tout changement de majeure** :
   `0.x → 1.0` n'est pas une mise à jour que `self-update` accepte ; la v1 se
   décidera à part, avec son propre chemin (tranché le 8 septembre 2026).
3. **Garder l'ancien binaire** à côté : `opencloud.prev`, un lien dur vers
   l'ancien fichier — rien n'est copié.
4. **Remplacer par écriture atomique** — fichier `opencloud.new` puis
   `rename`, jamais d'écrasement du fichier en cours d'exécution *(`text file
   busy`)*. Une mise à jour **par le paquet** retire le `.prev` : le retour
   arrière est alors l'ancien `.deb`, pas un binaire d'avant.
5. **Redémarrer l'unité.**
6. **Au démarrage, sauvegarder la base**, puis appliquer les migrations
   embarquées, numérotées, jamais rejouées. **Si la sauvegarde échoue, la
   migration n'a pas lieu** : le service s'arrête en le disant, la base est
   intacte, et le retour arrière est la commande affichée par `self-update`
   *(Cloudron a migré sans sauvegarde ; on ne migre pas du tout)*. Seules les
   dernières sauvegardes de migration sont gardées — le journal dit combien et
   lesquelles sont retirées : une sauvegarde par migration, sur une boucle de
   redémarrage, remplit le disque.

**La migration prend son propre verrou** : le fichier `migration.lock` dans le
répertoire d'état, posé juste avant la sauvegarde, retiré après la dernière
migration appliquée — et seulement s'il y a des migrations en attente. Il porte
le pid et la date de qui l'a posé. Une seconde instance, ou un redémarrage par
`apt-daily-upgrade` au milieu, trouve le verrou et **refuse de démarrer** : ni
sauvegarde, ni migration.

Un verrou resté est donc une migration interrompue ou échouée. Il n'est jamais
retiré tout seul, c'est voulu : le refus le nomme au démarrage et dans
`opencloud status`, avec la date, le processus et le chemin. Le geste :
vérifier qu'aucun autre openCloud ne tourne (`systemctl status opencloud`,
`pgrep opencloud`), puis retirer le fichier. La base n'a rien perdu — chaque
migration est une transaction — et le démarrage suivant reprend la migration
en attente.

**Le binaire en place fait foi, pas la base dpkg.** `self-update` n'annonce rien
à dpkg : `dpkg-query` reste sur la version posée par le dernier paquet. Poser un
`.deb` plus ancien que le binaire déjà là est donc un retour en arrière que dpkg
ne voit pas ; le paquet le refuse en le nommant, et donne le geste qui le lève
(`sudo env OPENCLOUD_ALLOW_DOWNGRADE=1 apt install ./opencloud_X.Y.Z_amd64.deb`,
après avoir remis la sauvegarde d'avant migration). Rien ne recule en silence sur
un schéma déjà migré.

**L'unité renonce au bout de cinq échecs de démarrage** au lieu de repartir sans
fin : chaque démarrage sauvegarde la base avant de migrer, et une migration qui
échoue toujours remplirait `/var/lib` — openCloud emporterait les autres services
de la machine, ce que « il n'est pas indispensable » interdit. L'opérateur lit
alors `failed` dans `systemctl status opencloud`.

Retour arrière : remettre `opencloud.prev` et la sauvegarde de base prise
avant migration. Ça se fait à la main, en deux commandes que l'interface
affiche. **Remettre le binaire sans sa sauvegarde ne suffit pas** : une base
qui porte une migration que le binaire ne connaît pas ne s'ouvre pas, le
service refuse de démarrer et dit quelle sauvegarde restaurer — sinon l'ancien
code tournerait en silence sur un schéma plus neuf que lui.

## Ce qu'on ne fait pas

- **Pas de mise à jour automatique silencieuse.** L'interface signale la
  version disponible ; le geste est explicite. *(Netdata l'active par défaut,
  Coolify l'a payé.)*
- **Jamais par la file d'actions** : la mise à jour n'est pas une action.
- **Le bouton se désactive au premier clic** — un double-clic sur Coolify a
  lancé deux mises à jour en parallèle et perdu la clé de chiffrement.
- **Pas de migration pendant qu'un autre processus peut redémarrer le
  service** : `apt-daily-upgrade` a coupé une migration Cloudron au milieu.
  Chaque migration est une transaction — coupée, elle n'a pas eu lieu — et le
  verrou de migration dit qu'une autre instance migre (plus haut).

## Piège connu à ne pas reproduire

`ProtectSystem=strict` dans l'unité **verrouille SQLite** si le dossier d'état
n'est pas déclaré en écriture *(Headscale #1274)*. L'unité openCloud déclare
`StateDirectory=opencloud` et `ReadWritePaths=` explicites — durcie, mais
capable d'écrire là où elle doit.

## Désinstaller

Deux gestes, deux résultats, dits avant :

| Geste | Ce qui part | Ce qui reste |
|---|---|---|
| `sudo apt remove opencloud` | Le binaire, son `.prev`, l'unité | `/etc/opencloud`, `/var/lib/opencloud` — base, clés, jetons — et **l'utilisateur système**, qui possède ces fichiers : le retirer les rendrait orphelins. Réinstaller retrouve tout |
| `sudo apt purge opencloud` | Tout ce qui précède **et** `/etc/opencloud`, `/var/lib/opencloud`, l'utilisateur | Rien d'openCloud sur cette machine. `apt purge` retire `/var/lib/opencloud` ; un `state_dir` déplacé reste en place, **à retirer à la main** — le script de purge ne lit pas le conffile |

Ces deux gestes, la mise à jour et la réinstallation sont **joués en CI** à
chaque changement, sur deux systèmes : sur le runner Ubuntu
(`packaging/test-install.sh`, puis `packaging/test-action.sh`) et dans un
conteneur Debian 12 avec systemd (`make package-test`, la même cible qu'en
local) — jamais sur le poste de travail.

**Ce qu'aucun des deux ne touche, jamais** : `/srv` — les services et leurs
données —, Traefik, CrowdSec, Docker, le collecteur. Ils appartiennent à la
machine, pas à openCloud ; ils continuent de tourner exactement comme avant.
C'est le principe « il n'est pas indispensable » (`01-perimetre.md`), vérifié
au moment où on l'enlève.

**Les machines gérées ne sont pas touchées** par la désinstallation de la
machine openCloud : un paquet ne parle pas au réseau. Ce qu'openCloud y a posé
— l'utilisateur `opencloud`, sa clé, sa règle `sudo`, le lanceur — se retire
**par l'action *Retirer une machine***, une par une, **avant** de désinstaller
openCloud lui-même. L'interface le rappelle : « N machines encore enrôlées ».

*Retirer une machine* écrit d'abord son marqueur « retrait en cours », prend un
recensement du disque, retire dans l'ordre inverse de la pose, reprend un
recensement, et montre la différence — rien ne disparaît en silence, rien
d'oublié (`15-catalogue-actions.md`).
