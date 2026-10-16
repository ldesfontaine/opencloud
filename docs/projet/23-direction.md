# Direction : d'observateur à plan de contrôle

> Brouillon du 16 octobre 2026, issue #153. Rien n'est tranché ici : chaque
> thème dit ce qui existe, ce qu'on vise, l'écart, le coût, et ce que Lucas
> doit décider. Les décisions se prennent une par une, dans l'ordre du
> [dernier tableau](#décisions-à-prendre). Ce document ne décrit pas
> l'application : c'est [`22-fonctionnement.md`](22-fonctionnement.md) qui le
> fait, et il y renvoie.

## Ce qui change, ce qui ne change pas

| | Aujourd'hui | Cible |
| --- | --- | --- |
| Rôle | openCloud **observe**, n'agit jamais sur une machine (README, « Fait / Ne fait pas ») | openCloud **déclare** ce qui doit exister, **compare** au réel, puis **agit** par gestes simples, un par un, chacun décidé à part |
| Ce qu'il n'est pas | — | Ni Coolify ni Portainer ; il ne remplace pas le SSH ; pas d'ordonnanceur ; pas de haute disponibilité |
| Ce qui reste | Un binaire, SQLite, front embarqué ; agent sortant, clé Ed25519 ; Docker lu, jamais écrit par la lecture ; mode `forward` derrière un SSO ; MCP à portées `read` / `write` | Tout cela reste. L'écriture arrive **à côté** de la lecture, dans des actions typées, jamais en ouvrant la lecture |
| Principe nouveau | — | **openCloud tombé = on ne voit plus, on n'agit plus, rien ne s'arrête.** Ce sont Docker et systemd qui gardent les services vivants, jamais openCloud |

## 1. Installation d'openCloud et arborescence

**Aujourd'hui** ([`installation.md`](../installation.md)) : binaire statique dans
`/usr/local/bin`, config dans `/etc/opencloud/config.toml`, état dans
`/var/lib/opencloud` (`StateDirectory=`), serveur sous l'utilisateur
`opencloud`, agent en **root** (`/proc`, volumes montés, socket Docker). Les
réglages vivent à quatre endroits : `config.toml`, `settings.toml`,
`lifecycle.json`, la base ([« Ce qui est stocké »](22-fonctionnement.md#ce-qui-est-stocké-et-où)).
Aucune mise à jour automatique, ni du serveur ni des agents.

**Cible** : un contrat d'installation écrit, identique sur toute machine.

| Chemin | Contenu | Aujourd'hui |
| --- | --- | --- |
| `/usr/local/bin/opencloud` | le binaire, serveur ou agent | pareil |
| `/etc/opencloud/config.toml` | **le seul** fichier de réglages | `settings.toml` et `lifecycle.json` à ramener dans la base ou dans ce fichier |
| `/var/lib/opencloud/` | la base, les sauvegardes de la base, l'identité de l'agent, sa rétention locale | la base et `identity.json` ; le reste n'existe pas |
| `/srv/opencloud/<service>/` | `compose.yml`, `.env`, `data/` : un service déployé par openCloud | n'existe pas |

**Écart et coût**

| Changement | Où | Pourquoi | Coût |
| --- | --- | --- | --- |
| Fusionner `settings.toml` et `lifecycle.json` | `internal/settings`, `internal/lifecycle` | Un nouvel utilisateur ne doit chercher qu'à un endroit ; `export` doit tout sortir | Moyen : migration des réglages existants |
| Mise à jour d'un **agent** depuis le serveur | agent, nouvelle action | Le geste le plus utile et le moins dangereux : premier candidat à « agir » | Moyen : télécharger, vérifier la somme, remplacer, redémarrer |
| Le **serveur** ne se met jamais à jour seul | doc | Il porte la base ; une montée ratée casse tout | Nul |
| Agent root ou non | `opencloud-agent.service` | Lire `/proc` et Docker en root est accepté aujourd'hui ; écrire dans `/srv/opencloud` en root l'est moins | À décider (thème 5) |

## 2. Déclarer : services, export, `apply`

**Aujourd'hui** : aucune entité « service déclaré ». Le seul regroupement
machine + conteneur + sonde + tâche est le **composant** de la page de statut
(`status_components`, `status_component_members`). Sondes, tâches, composants,
canaux se créent **dans l'interface ou par l'API**, uniquement ; pas d'import,
pas d'export ([« Ce qui manque encore »](22-fonctionnement.md#ce-qui-manque-encore)).
Une sonde **ne s'édite pas** : on la supprime et on la recrée, et son
historique (`probe_days`) part avec. L'agent ne lit que les labels Compose,
aucun label `opencloud.*`.

**Cible** : la base reste l'état courant ; **un fichier** est la vérité
reproductible.

| Pièce | Ce qu'elle fait | Modèle vu ailleurs |
| --- | --- | --- |
| Service déclaré | nom, machine, image ou binaire, version, ports, adresse publique, protégé ou non, sauvegardé ou non, composant de statut. Générique : rien sur les zones ou les projets d'une infra donnée | Komodo (`[[stack]]`, `[[server]]`) |
| `opencloud export` | sort tout ce qui est déclaré en YAML lisible : machines, services, sondes, tâches, composants, canaux, réglages | Komodo `ExportAllResourcesToToml`, Beszel `config-yaml` |
| `opencloud apply <fichier>` | calcule le diff, **l'affiche, demande confirmation**, crée ou met à jour. Ne supprime jamais ce qui n'est pas déclaré sans option explicite | Komodo Resource Sync (`delete` opt-in) ; piège Beszel (supprime au démarrage) |
| Vue « écart » | par service : déclaré et absent, présent et non déclaré, conforme. Dans l'interface et par un outil MCP de lecture | — |

**Écart et coût**

| Changement | Où | Pourquoi | Coût |
| --- | --- | --- | --- |
| Éditer une sonde sans la recréer | `internal/probe`, `PUT /api/probes/{id}` | Sans ça, chaque `apply` efface 90 jours d'historique | Faible, prérequis de tout le reste |
| Table `declared_services` et liens | migration, `internal/…` | Le composant de statut est public ; il ne peut pas porter l'image, la version, les chemins | Moyen |
| `export` / `apply` | `cmd/opencloud`, API | Reproductibilité : perdre la machine openCloud ne doit coûter qu'un `apply` | Moyen à fort : le diff et sa confirmation sont le vrai travail |
| Labels `opencloud.*` lus par l'agent | `internal/dockerapi`, `dockerwatch` | Déclarer là où le service vit ; déjà noté « plus tard » | Faible, **après** le fichier |

Uptime Kuma a retiré son import/export en v2 et dit de son API interne
« breaking changes may occur » : c'est le piège exact d'une config qui ne vit
qu'en base.

## 3. Déployer un service

**Aujourd'hui** : rien. La commande de mise à jour d'une image est fabriquée,
jamais jouée ([« Comment une image est comparée »](22-fonctionnement.md#comment-une-image-est-comparée-à-son-registre)).

**Cible** : un geste de base, avant tout catalogue : *déployer ce compose sur
cette machine, dans l'arborescence fixe.*

| Question | Proposition | Pourquoi | Contrepartie |
| --- | --- | --- | --- |
| Version | **épinglée** à l'installation ; openCloud **propose** la montée (il la détecte déjà), l'opérateur valide | `latest` = mise à jour subie au redémarrage d'un conteneur | Il faut valider à la main ; c'est voulu |
| Docker seulement ? | **Oui pour ce qu'openCloud déploie.** Il continue de superviser des binaires (lui-même, ses agents, Forgejo…) | Un seul mécanisme de déploiement à maintenir | Un binaire ne se déploie pas par openCloud |
| Volumes | **bind mounts** sous `/srv/opencloud/<service>/data/`, jamais de volume nommé | Lisible, sauvegardable par `tar`, retrouvable en SSH sans openCloud | Droits de fichiers à gérer (UID du conteneur) |
| Choix de la machine | **à la main** | Un ordonnanceur, c'est Nomad, pas openCloud | Aucune répartition automatique, jamais |
| Prérequis vérifiés | agent présent avec la capacité `docker.deploy`, Docker installé, espace disque suffisant | Rien d'autre : CPU et RAM ne se réservent pas | — |
| Icônes | label `org.opencontainers.image.*` si présent, sinon icône générique, sinon dépôt par l'opérateur. Pas de récupération de logos sur le web | Droits, réseau, dette pour rien | À faire en dernier |

**Coût** : fort. C'est le premier geste qui écrit sur une machine ; il dépend
des thèmes 5 (agent) et 7 (journal), qui passent avant.

## 4. Modules et dépendances

**Aujourd'hui** : rien.

**Cible**, après le thème 3 seulement : un module = un dossier avec un
`compose.yml` et un `module.yml` :

| Clé | Contenu |
| --- | --- |
| `name`, `version` | — |
| `provides` | ce que le module offre : `sso`, `proxy`, `tls`, `backup-target`… |
| `requires` | ce qu'il lui faut, chacun **optionnel ou non** |
| `ports`, `volumes`, `env` | avec, pour `env`, le schéma des variables attendues |
| `backup` | les chemins à sauvegarder |
| `hooks` | des branchements déclaratifs : « si `sso` existe, ajouter ces labels » |

openCloud déduit l'ordre possible et **propose** les branchements dans les deux
sens : installer un coffre d'abord et le mettre derrière le SSO plus tard, ou
l'inverse. Règle : **jamais de code Go par application** ; sinon chaque app
est une dette. Le catalogue peut n'être qu'un dépôt Git de dossiers.

**Coût** : fort, et nul tant que le thème 3 n'est pas livré.

## 5. L'agent : actions, capacités, clés

**Aujourd'hui** ([« Comment une machine entre »](22-fonctionnement.md#comment-une-machine-entre),
issue #146) : l'agent **initie** la connexion, signe un défi, tient un flux. Il
n'exécute que **quatre commandes** du serveur : `probes`, `logs`, `logs_stop`,
`image_checks` ; aucun shell, aucune écriture Docker. Le serveur vérifie
l'agent ; **l'agent ne vérifie pas le serveur**. Pas de capacités : chaque
agent accepte les quatre commandes. Le jeton d'enrôlement vaut 24 h, sert une
fois. L'agent tourne en root.

**Cible**

| Pièce | Proposition | Modèle vu ailleurs |
| --- | --- | --- |
| Actions | une **liste fermée, typée** (`type` + `params`, un identifiant d'exécution) ; jamais une chaîne shell dans le protocole | Komodo `PeripheryRequest` (~100 variantes), Beszel `WebSocketAction` (10) |
| Capacités | déclarées **dans le fichier de l'agent**, pas dans l'interface : `observe` (toujours), `docker.deploy`, `docker.update`, `files.write` limité à des chemins, `ssh.keys` limité à des comptes, `agent.update`. Le serveur demande, la machine décide ; le refus est journalisé | Komodo `disable_terminals`, `core_public_keys` ; Netdata `allow from` |
| Vérification réciproque | l'agent connaît la clé publique du serveur et vérifie chaque session ; un faux serveur ne commande rien | Komodo (clés `core_public_keys`), Beszel (le hub signe le jeton) |
| Enrôlement | garder le jeton à usage unique ; ajouter la **rotation** de la clé de l'agent et de celle du serveur | Teleport (jeton 30 min), Komodo `RotatePrivateKey`, Swarm (3 mois) |
| Root | à décider : rester root pour tout, ou un utilisateur `opencloud` avec Docker et `/srv/opencloud`, root seulement pour `/proc` et les volumes | — |

**Ce qu'on ne fait pas** : Coolify et Dokploy agissent par **SSH avec shell
libre et clé privée root stockée sur le serveur**. Pas de typage, audit
difficile, secrets dans les commandes.

**Coût** : moyen pour les capacités et la vérification réciproque ; c'est
#146, à fermer **avant** le premier geste.

## 6. Les données

**Aujourd'hui** : tout vit sur la machine openCloud, dans `opencloud.db` :
brut toutes les 10 s, moyennes horaires, journalières, avec leurs purges
([« Ce qui est stocké »](22-fonctionnement.md#ce-qui-est-stocké-et-où)).
L'agent **ne garde rien** : flux coupé, mesures perdues. La base n'est
sauvegardée que si l'hôte l'est (chez Lucas : `vzdump` de la VM). Les jetons de
ping sont en clair dans la base.

**Cible**

| Où | Quoi | Combien de temps | Pourquoi |
| --- | --- | --- | --- |
| Serveur, `opencloud.db` | les données principales, inchangées | purges actuelles | — |
| Agent, `/var/lib/opencloud/agent/local.db` | un **petit SQLite** : mesures, résultats de sondes, dernier jeu de sondes, dernier état désiré | **24 h**, anneau | Un tampon en mémoire disparaît au redémarrage, et c'est au redémarrage qu'on veut les données. Quelques Mo |
| Agent → serveur | **rejeu** des échantillons manqués à la reconnexion, fenêtre bornée à 24 h | — | Netdata (`replication period`), Centreon (fichiers de rétention rejoués) |
| Serveur, `/var/lib/opencloud/backups/` | copie **à chaud** de la base chaque nuit, N copies gardées ; option : poussée vers un autre agent | à régler | Sans ça, la base-vérité n'a pas de filet. Fait partie du design, pas d'un chapitre à côté |

**Écart et coût** : rétention locale = moyen (un deuxième SQLite dans l'agent,
un protocole de rejeu) ; sauvegarde de la base = faible (`VACUUM INTO` ou
l'API de sauvegarde SQLite) ; hacher les jetons de ping = à décider, la page
doit pouvoir réafficher l'URL.

## 7. Journal d'actions et notifications

**Aujourd'hui** : aucune action, donc aucun journal. Les **canaux**
(Discord, Slack, Mattermost, ntfy, JSON signé, e-mail, Matrix) existent pour les
alertes ([« Comment une alerte naît »](22-fonctionnement.md#comment-une-alerte-naît-se-tait-et-part)).

**Cible** : dès le **premier** geste, un enregistrement par action : qui
(identité de l'opérateur, ou MCP, ou règle), quoi, quand, sur quelle machine,
paramètres sans secret, résultat, journal d'exécution. Stocké dans la base,
**et** envoyé sur un canal « audit » : une base effacée n'efface pas la trace.
Les canaux existants servent tels quels.

Modèles : Komodo (`Update` : opération, opérateur, cible, succès, logs),
Teleport (un événement JSON par ligne, fichier qui « never deletes », export HTTP
générique).

**Coût** : faible, à condition de le faire avant le thème 3, pas après.

## 8. Quand le serveur tombe

**Aujourd'hui** : rien ne s'arrête (openCloud n'agit pas) ; l'interface, les
alertes et la page de statut sont mortes ; les agents reviennent seuls avec un
délai croissant ; les mesures de la coupure sont perdues.

**Cible et positions**

| Question | Position | Pourquoi |
| --- | --- | --- |
| Les services continuent ? | **Oui, toujours** : Docker et systemd les tiennent ; l'agent garde le dernier état désiré et continue de l'appliquer | Nomad et Swarm : « les workloads continuent sans quorum » ; Portainer Edge retente une stack une semaine |
| Les agents se parlent entre eux ? | **Non.** Agent → serveur seulement | Un maillage multiplie la complexité et la surface d'attaque ; Netdata et Centreon ne le font pas |
| L'interface pendant la panne ? | **Morte, et c'est accepté** | Pour un seul opérateur, un serveur de secours coûte plus qu'il ne rapporte |
| Ce qu'on garantit à la place | **la remise en route rapide** : services vivants, 24 h de données chez chaque agent, base sauvegardée ailleurs, `apply` et ré-enrôlement remontent tout en dix minutes | — |
| Et plus tard ? | l'architecture n'interdit pas un second serveur en veille (Netdata : parents multiples avec bascule) ; on ne le construit pas | — |

## 9. Ce que l'étude des autres outils apprend

Étude du 16 octobre 2026 : Komodo, Beszel, Portainer Edge, Netdata, Gatus,
Uptime Kuma, Dokploy, Coolify, Teleport, Nomad, Swarm, Centreon.

| À reprendre | De qui |
| --- | --- |
| Liste d'actions fermée et typée, identifiant d'exécution | Komodo, Beszel |
| L'agent déclare ce qu'il accepte, dans son fichier | Komodo, Netdata |
| Vérification réciproque des clés ; rotation ; jeton d'enrôlement court | Komodo, Beszel, Teleport, Swarm |
| Fichier déclaratif + diff affiché + confirmation ; suppression opt-in ; export de l'état courant | Komodo, Gatus, Beszel |
| Rechargement d'une config invalide refusé, l'ancienne reste en service | Gatus |
| Journal structuré par action, exporté en JSON vers n'importe quel récepteur | Komodo, Teleport |
| Chaque agent garde ses données et rejoue les trous | Netdata, Centreon |
| L'état désiré reste chez l'agent, qui retente longtemps | Portainer Edge, Nomad |

| À éviter | Vu chez |
| --- | --- |
| Base externe obligatoire (Mongo, Postgres) | Komodo, Dokploy |
| SSH avec shell libre et clé root sur le serveur comme seul mécanisme | Coolify, Dokploy |
| Config en base seulement, sans export stable | Uptime Kuma (export retiré en v2) |
| Agent sans aucune rétention locale | Beszel, Komodo Periphery |
| Suppression implicite à la synchronisation | Beszel |

## 10. Ordre proposé

| Étape | Issue | Ce qu'elle livre | Écrit sur une machine ? |
| --- | --- | --- | --- |
| 1 | #153 | la direction dans la doc ; édition des sondes ; services déclarés ; `export` / `apply` ; vue « écart » | non |
| 2 | #146 | capacités côté agent ; vérification réciproque ; rotation | non |
| 3 | à ouvrir | journal d'actions + canal « audit » ; sauvegarde de la base | non |
| 4 | à ouvrir | rétention locale de l'agent et rejeu | non |
| 5 | à ouvrir | **premier geste** : mettre à jour un agent depuis le serveur | oui |
| 6 | #125 | appliquer une mise à jour d'image validée | oui |
| 7 | à ouvrir | déployer un compose dans `/srv/opencloud/<service>/` | oui |
| 8 | #152 | clés SSH distribuées | oui |
| 9 | à ouvrir | modules et dépendances ; catalogue | — |

Rien ne passe à l'étape 5 tant que 2 et 3 ne sont pas fermées.

## Décisions à prendre

Par Lucas, une par une. Mon avis est donné ; la colonne « Décision » est vide
tant qu'il n'a pas tranché.

| N° | Question | Options | Mon avis | Décision |
| --- | --- | --- | --- | --- |
| 1 | Un seul fichier de réglages ? | garder `settings.toml` + `lifecycle.json` ; tout ramener dans `config.toml` + base | tout ramener | |
| 2 | L'agent reste root ? | root pour tout ; utilisateur `opencloud` + root ciblé | utilisateur dédié, root ciblé, si le coût reste raisonnable | |
| 3 | Édition des sondes avant tout ? | oui ; non, on recrée | oui | |
| 4 | Format du fichier déclaratif | YAML ; TOML | YAML, plus courant chez ceux qui écrivent des compose | |
| 5 | `apply` supprime ce qui n'est pas déclaré ? | jamais ; option explicite ; toujours | option explicite, jamais par défaut | |
| 6 | Version `latest` autorisée au déploiement ? | jamais ; avec avertissement | jamais | |
| 7 | Volumes | bind mounts dans `/srv/opencloud` ; volumes nommés ; au choix | bind mounts seulement | |
| 8 | Docker seulement pour déployer ? | oui ; aussi des binaires | oui | |
| 9 | Liste de capacités de départ | la liste du thème 5 ; autre | la liste du thème 5 | |
| 10 | Capacités dans le fichier de l'agent ou dans l'interface ? | fichier ; interface ; les deux | fichier, l'interface ne fait que les lire | |
| 11 | Rétention locale de l'agent | SQLite 24 h ; tampon mémoire ; rien | SQLite 24 h | |
| 12 | Sauvegarde de la base | copie locale ; copie + poussée vers un agent ; rien | copie locale d'abord, poussée ensuite | |
| 13 | Jetons de ping | rester en clair ; hacher et ne plus réafficher | à voir, bas dans la liste | |
| 14 | Premier geste | mise à jour d'un agent ; mise à jour d'image ; déploiement | mise à jour d'un agent | |
| 15 | Agents entre eux | jamais ; plus tard | jamais | |
| 16 | Serveur de secours | non ; plus tard ; maintenant | non, l'architecture ne l'interdit pas | |
| 17 | Icônes | OCI + générique + dépôt ; bibliothèque embarquée ; logos récupérés | OCI + générique + dépôt, en dernier | |
