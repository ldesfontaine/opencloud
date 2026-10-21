# Direction : d'observateur à plan de contrôle

> Rédigé le 16 octobre 2026 (issue #153). **Décisions prises par Lucas le
> 17 octobre 2026** : elles sont dans les tableaux [« Décisions »](#décisions),
> [« Décisions du tri des fonctionnalités »](#décisions-du-tri-des-fonctionnalités)
> et [« Points techniques tranchés »](#points-techniques-tranchés). Chaque
> thème dit ce qui existe, ce qu'on vise, l'écart et le coût. Le modèle de
> données qui en découle est dans [`24-modele-cible.md`](24-modele-cible.md).
> Ce document ne décrit pas l'application : c'est
> [`22-fonctionnement.md`](22-fonctionnement.md) qui le fait, et il y renvoie.

## Ce qui change, ce qui ne change pas

| | Aujourd'hui | Cible |
| --- | --- | --- |
| Rôle | openCloud **observe**, n'agit jamais sur une machine (README, « Fait / Ne fait pas ») | openCloud **déclare** ce qui doit exister, **compare** au réel, puis **agit** par gestes simples, un par un, chacun décidé à part |
| But | — | L'opérateur crée une VM ou un LXC, installe l'agent par une ligne générée par openCloud, et gère tout ou presque depuis openCloud |
| Ce qu'il n'est pas | — | Ni Coolify ni Portainer ; il ne remplace pas le SSH ; pas d'ordonnanceur ; pas de haute disponibilité. Raisonné comme un outil générique, jamais pour une infra particulière |
| Ce qui reste | Un binaire, SQLite, front embarqué ; agent sortant, clé Ed25519 ; Docker lu, jamais écrit par la lecture ; mode `forward` derrière un SSO ; MCP à portées `read` / `write` | Tout cela reste ; le MCP passe en lecture seule pendant la refonte. L'écriture arrive **à côté** de la lecture, dans des actions typées, jamais en ouvrant la lecture |
| Ce qui se revoit | — | Les fonctionnalités existantes ne sont pas sacrées : le [tri](#décisions-du-tri-des-fonctionnalités) dit ce qui change |
| Principe nouveau | — | **openCloud tombé = on ne voit plus, on n'agit plus, rien ne s'arrête.** Ce sont Docker et systemd qui gardent les services vivants, jamais openCloud |

## 1. Installation d'openCloud et arborescence

**Aujourd'hui** ([`installation.md`](../installation.md)) : binaire statique dans
`/usr/local/bin`, config dans `/etc/opencloud/config.toml`, état dans
`/var/lib/opencloud` (`StateDirectory=`), serveur sous l'utilisateur
`opencloud`, agent en **root** (`/proc`, volumes montés, socket Docker). Les
réglages vivent à quatre endroits : `config.toml`, `settings.toml`,
`lifecycle.json`, la base ([« Ce qui est stocké »](22-fonctionnement.md#ce-qui-est-stocké-et-où)).
La machine openCloud est mesurée par un chemin intégré à `serve`, pas par un
agent. Aucune mise à jour automatique, ni du serveur ni des agents.

**Cible** : un contrat d'installation écrit, identique sur toute machine, la
machine openCloud comprise : elle reçoit un agent comme les autres.

| Chemin | Contenu | Aujourd'hui |
| --- | --- | --- |
| `/usr/local/bin/opencloud` | le binaire, serveur ou agent | pareil |
| `/etc/opencloud/config.toml` | ce que l'admin pose avant le démarrage ; **lecture seule** pour le serveur | `settings.toml` disparaît, vers la base ; `lifecycle.json` reste un cache, hors export |
| `/var/lib/opencloud/` | la base, l'identité de l'agent, sa rétention locale | la base et `identity.json` ; la rétention n'existe pas |
| `/srv/workspace/<service>/` | `compose.yml`, `.env`, conf ; `data/` : un service déployé par openCloud. Racine réglable dans le fichier de l'agent à l'installation ; fichier témoin `.opencloud` | n'existe pas |

Le nom du produit n'est pas définitif : les chemins, labels, préfixe de jeton
et en-têtes qui le portent passent par un seul paquet de constantes
([décision « Nom »](#autres-décisions-actées)).

**Écart et coût**

| Changement | Où | Pourquoi | Coût |
| --- | --- | --- | --- |
| Ramener `settings.toml` dans la base | `internal/settings` | Un nouvel utilisateur ne cherche qu'à un endroit ; `export` sort tout | Moyen : migration des réglages existants |
| Nom à un seul endroit | constante d'affichage + catalogues `internal/lang` ; paquet de constantes techniques | Changer de nom sans chasser chaque occurrence | Faible à moyen : relever les occurrences une fois |
| Agent installé par une **ligne générée** dans l'interface | serveur, script d'installation | `curl … \| sh`, aussi en cloud-init ; jeton à usage unique lu sur stdin ou dans un fichier, jamais en argv. Le serveur n'a jamais d'accès SSH | Moyen : le script, la page qui le génère |
| Racine déjà occupée | script d'installation | Si le dossier existe et n'est pas vide, l'installation s'arrête et demande un autre chemin | Faible |
| Machine openCloud avec un agent | `serve`, migration | Un seul mécanisme : le chemin intégré disparaît | Faible ; la machine garde son id et son historique, mais ne se voit plus tant que l'agent n'est pas installé |
| Mise à jour d'un **agent** depuis le serveur | agent, nouvelle action | Le geste le plus utile et le moins dangereux : premier geste (décision 14) | Moyen : télécharger, vérifier la somme, remplacer, redémarrer |
| Le **serveur** ne se met jamais à jour seul | doc | Il porte la base ; une montée ratée casse tout | Nul |
| Agent root ou non | `opencloud-agent.service` | Lire `/proc` et Docker en root est accepté aujourd'hui ; écrire dans `/srv/workspace` en root l'est moins | Reportée avant l'étape 2 (décision 2) |

## 2. Déclarer : l'annuaire, export, `apply`

**Aujourd'hui** : aucune entité « service déclaré ». L'identifiant d'un
service est machine + conteneur : un conteneur recréé perd son composant de
statut, sa sonde, sa politique de mise à jour. Le seul regroupement
machine + conteneur + sonde + tâche est le **composant** de l'unique page de
statut (`status_components`, `status_component_members`). Sondes, tâches,
composants, canaux se créent **dans l'interface ou par l'API**, uniquement ;
pas d'import, pas d'export ([« Ce qui manque encore »](22-fonctionnement.md#ce-qui-manque-encore)).
Une sonde et une tâche s'éditent sur place (voir
[« Comment une cible est sondée »](22-fonctionnement.md#comment-une-cible-est-sondée)) :
l'historique et le jeton restent. L'agent lit les labels Compose et ceux du
produit : il transmet toutes les clés `<préfixe>.*` bornées (16 au plus), le
serveur n'en garde que deux, `<préfixe>.service` et `<préfixe>.unit`. Les 33 clés étrangères sont toutes `cascade` ou
`set null` : supprimer une machine vide 17 tables.

**Cible** : la base reste l'état courant ; **un fichier** est la vérité
reproductible. **L'annuaire** = tout ce que décrit ce fichier : machines,
projets, services déclarés et leurs unités, sondes, tâches, canaux, pages de
statut. Le **service déclaré** en est l'objet central.

| Pièce | Ce qu'elle fait | Modèle vu ailleurs |
| --- | --- | --- |
| Service déclaré | identité propre, jamais tirée de Docker ; un dossier avec ses unités (service Compose, conteneur, binaire) ; machine, nature (application ou outil d'infra), image et version épinglée par unité, ports, adresse publique, protégé ou non. Générique : rien de propre à une infra donnée | Komodo (`[[stack]]`, `[[server]]`) |
| Projet | regroupe des services ; sert à la page de statut, au routage des canaux, au filtre de l'interface, plus tard aux droits | — |
| Page de statut | une par projet, créée à la main ; remplace la page globale | — |
| `opencloud export` | sort l'annuaire en YAML lisible, `id` + `name`, **sans aucun secret** (marqueur « conservé ») ; peut aller dans Git | Komodo `ExportAllResourcesToToml`, Beszel `config-yaml` |
| `opencloud apply <fichier>` | calcule le diff, **l'affiche, demande confirmation**, crée ou modifie sur place, ne recrée jamais. Ne supprime ce qui n'est pas déclaré que sur option explicite | Komodo Resource Sync (`delete` opt-in) ; piège Beszel (supprime au démarrage) |
| Vue « écart » | par service : déclaré et absent, présent et non déclaré (adoptable d'un clic), conforme. Dans l'interface et par un outil MCP de lecture | — |

**Écart et coût**

| Changement | Où | Pourquoi | Coût |
| --- | --- | --- | --- |
| Éditer sur place sondes et tâches | `internal/probe`, `internal/heartbeat`, API | Sans ça, chaque `apply` efface 90 jours d'historique ou change un jeton | Faible, prérequis de tout le reste. **Fait** (PR « édition sur place ») |
| Annuaire : services, unités, projets, nature | migration, `internal/…` | Le composant de statut est public ; il ne peut pas porter l'image, la version, les chemins | Moyen ; un rapprochement conteneur → unité à chaque inventaire |
| Suppressions en `restrict` + plan | migration, chaque suppression | La base refuse toute perte silencieuse | Moyen : chaque suppression écrit son plan |
| `export` / `apply` | `cmd/opencloud`, API | Reproductibilité de l'annuaire | Moyen à fort : le diff et sa confirmation sont le vrai travail |
| Pages de statut par projet | `internal/status`, front | La page globale ne sert à rien | Moyen ; la page actuelle devient un projet |
| Labels `<préfixe>.*` lus par l'agent | `internal/dockerapi`, `dockerwatch` | Rattacher ce qu'openCloud déploie sans ambiguïté ; préfixe tiré du paquet de constantes | Faible, **après** le fichier. **Fait** : annuaire 7/7 (lus, pas encore posés par un déploiement) |

Uptime Kuma a retiré son import/export en v2 et dit de son API interne
« breaking changes may occur » : c'est le piège exact d'une config qui ne vit
qu'en base.

## 3. Déployer un service

**Aujourd'hui** : rien. La commande de mise à jour d'une image est fabriquée,
jamais jouée ([« Comment une image est comparée »](22-fonctionnement.md#comment-une-image-est-comparée-à-son-registre)).

**Cible** : un geste de base, avant tout catalogue : *déployer ce compose sur
cette machine, dans l'arborescence fixe.*

| Question | Position | Pourquoi | Contrepartie |
| --- | --- | --- | --- |
| Version | **épinglée** ; choisir « dernière version » écrit le numéro du jour. openCloud **propose** la montée, ne l'applique jamais seul | `latest` = mise à jour subie au redémarrage d'un conteneur | Il faut valider à la main ; c'est voulu |
| Docker seulement ? | **Oui pour ce qu'openCloud déploie**, dans un premier temps. Il continue de superviser des binaires (lui-même, ses agents, Forgejo…) | Un seul mécanisme de déploiement à maintenir | Un binaire ne se déploie pas par openCloud |
| Volumes | **bind mounts** sous `/srv/workspace/<service>/data/`, jamais de volume nommé | Lisible, sauvegardable par `tar`, retrouvable en SSH sans openCloud | Droits de fichiers à gérer (UID du conteneur) |
| Secrets | le `.env` sur la machine fait foi ; openCloud ne le recopie pas en base | Une seule source de vérité | Restaurer un service passe par sa sauvegarde : `data/` + `.env`, chiffrés |
| Protection | tout protégé par défaut : forward-auth au proxy, OIDC optionnel par modèle ; chemins ouverts listés et affichés comme publics | Rien d'exposé par oubli | Une liste de chemins à tenir par service |
| Choix de la machine | **à la main** | Un ordonnanceur, c'est Nomad, pas openCloud | Aucune répartition automatique, jamais |
| Prérequis vérifiés | agent présent avec le droit de déployer, Docker installé, espace disque suffisant | Rien d'autre : CPU et RAM ne se réservent pas | — |
| Icônes | label `org.opencontainers.image.*` si présent, sinon icône générique, sinon dépôt par l'opérateur. Pas de récupération de logos sur le web | Droits, réseau, dette pour rien | À faire en dernier |

**Coût** : fort. C'est le premier geste qui écrit des services sur une
machine ; il dépend des thèmes 5 (agent) et 7 (journal), qui passent avant.

## 4. Modules et dépendances

**Aujourd'hui** : rien.

**Cible**, après le thème 3 seulement : un module = un dossier avec un
`compose.yml` et un `module.yml` :

| Clé | Contenu |
| --- | --- |
| `name`, `version` | — |
| `nature` | application ou outil d'infra, pré-réglée, modifiable |
| `provides` | ce qu'un outil d'infra offre : `sso`, `proxy`, `tls`, `backup-target`… |
| `requires` | ce qu'il lui faut, chacun **optionnel ou non** |
| `ports`, `volumes`, `env` | avec, pour `env`, le schéma des variables attendues |
| `open_paths` | les chemins ouverts pré-remplis, modifiables |
| `backup` | sauvegardé ou non ; le périmètre reste `data/` + `.env` (décision 7) |
| `hooks` | des branchements déclaratifs : « si `sso` existe, ajouter ces labels » |

La nature et ce que fournit un outil d'infra entrent dans l'annuaire dès
l'étape 1 : c'est sur quoi les modules se branchent. openCloud déduit l'ordre
possible et **propose** les branchements dans les deux sens : installer un
coffre d'abord et le mettre derrière le SSO plus tard, ou l'inverse. Règle :
**jamais de code Go par application** ; sinon chaque app est une dette. Le
catalogue peut n'être qu'un dépôt Git de dossiers, applications et outils
d'infra séparés.

**Coût** : fort, et nul tant que le thème 3 n'est pas livré.

## 5. L'agent : actions, droits, clés

**Aujourd'hui** ([« Comment une machine entre »](22-fonctionnement.md#comment-une-machine-entre),
issue #146) : l'agent **initie** la connexion, signe un défi, tient un flux
(SSE + POST). Il n'exécute que **quatre commandes** du serveur : `probes`,
`logs`, `logs_stop`, `image_checks`, au format `{Name, Payload any}`, dans une
file de 16 en mémoire ; aucun shell, aucune écriture Docker. Le serveur
vérifie l'agent ; **l'agent ne vérifie pas le serveur**. Les droits sont
cochés à l'installation, écrits dans le fichier de l'agent, rapportés et
affichés, **pas encore appliqués** : chaque agent accepte les quatre
commandes. Le jeton d'enrôlement vaut 1 h, sert une fois. L'agent tourne en
root. Machine et agent sont deux tables (`agents`, migration
`030_agents.sql`) ; un agent se révoque sans supprimer sa machine
([« Comment une machine entre »](22-fonctionnement.md#comment-une-machine-entre)).

**Cible**

| Pièce | Position | Modèle vu ailleurs |
| --- | --- | --- |
| Actions | une **liste fermée, typée** (`type` + `params`, un identifiant d'exécution) ; jamais une chaîne shell dans le protocole. Le format actuel se casse ; transport, enrôlement Ed25519 et relais des journaux restent | Komodo `PeripheryRequest` (~100 variantes), Beszel `WebSocketAction` (10) |
| Droits (capacités) | **pas de « modes »** : un seul agent, une liste de droits cochée dans l'interface à l'installation, écrite par la ligne générée dans le fichier local de l'agent. L'agent fait ce qu'il a le droit de faire, rien d'autre ; un refus est journalisé. Liste de départ : décision 9, **prise** ; changement après l'installation : décision 10, reportée. **Fait** : cochés, écrits, rapportés et affichés avec l'écart (030) ; les appliquer reste #146 | Komodo `disable_terminals`, `core_public_keys` ; Netdata `allow from` |
| Vérification réciproque | l'agent connaît la clé publique du serveur et vérifie chaque session ; un faux serveur ne commande rien | Komodo (clés `core_public_keys`), Beszel (le hub signe le jeton) |
| Enrôlement | jeton à usage unique, porté par la ligne générée ; **rotation** de la clé de l'agent et de celle du serveur. Machine et agent séparés : ré-enrôler, réinstaller ou tourner la clé garde la machine et son historique | Teleport (jeton 30 min), Komodo `RotatePrivateKey`, Swarm (3 mois) |
| Root | reporté avant l'étape 2 (décision 2). Piste : un observateur non privilégié + un exécuteur root étroit avec politique locale | — |
| Contrôles | après l'étape 2 : contrôles sur le modèle NRPE, commandes définies dans le fichier local de l'agent, le serveur ne demande qu'un nom ; code de sortie 0/1/2/3. Un genre de sonde, avec un état « avertissement » | Nagios NRPE |

**Ce qu'on ne fait pas** : Coolify et Dokploy agissent par **SSH avec shell
libre et clé privée root stockée sur le serveur**. Pas de typage, audit
difficile, secrets dans les commandes. openCloud n'a jamais d'accès SSH.

**Coût** : moyen pour les droits et la vérification réciproque ; c'est
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
| Serveur, `opencloud.db` | les données principales ; les secrets saisis dans l'interface **chiffrés** par la clé du serveur ; un historique long par service, sur son identité stable ; trafic sans les interfaces virtuelles (veth, ponts) ; un agrégat horaire par volume | purges actuelles | Une base volée sans la clé ne livre rien ; trafic compté deux fois et volume plein noyé dans la somme aujourd'hui |
| Agent, `/var/lib/opencloud/agent/local.db` | un **petit SQLite** : mesures, résultats de sondes, dernier jeu de sondes, sa part de l'annuaire | **24 h**, anneau | Un tampon en mémoire disparaît au redémarrage, et c'est au redémarrage qu'on veut les données. Quelques Mo |
| Agent → serveur | **rejeu** des échantillons manqués à la reconnexion, fenêtre bornée à 24 h | — | Netdata (`replication period`), Centreon (fichiers de rétention rejoués) |
| Destination de sauvegarde | la base, avec la clé du serveur ; chaque service (`data/` + `.env`). **Chiffrées**, envoyées vers une destination choisie : une machine avec agent, plus tard un stockage externe. **Pas de copie locale** ; sans destination, l'interface le dit | N copies gardées, à régler | Sans ça, la base-vérité n'a pas de filet. Une sauvegarde parmi d'autres, dans la page Sauvegardes |
| Chiffrement des sauvegardes | une paire de clés : la publique chiffre chaque nuit sur le serveur ; la privée, chiffrée par une phrase de passe, est déposée avec les sauvegardes | — | Sauvegarde automatique sans secret en clair sur le serveur ; seule la phrase est à garder de côté |

**Écart et coût** : rétention locale = moyen (un deuxième SQLite dans l'agent,
un protocole de rejeu) ; sauvegardes = moyen (envoi vers un agent, paire de
clés, page Sauvegardes) ; secrets chiffrés en base = faible ; jetons de ping
réaffichés en clair, stockés chiffrés, régénérables (décision 13) = faible,
une empreinte à côté pour retrouver la tâche sur un ping.

## 7. Journal d'actions, alertes et notifications

**Aujourd'hui** : aucune action, donc aucun journal. Les **canaux**
(Discord, Slack, Mattermost, ntfy, JSON signé, e-mail, Matrix) existent pour les
alertes ([« Comment une alerte naît »](22-fonctionnement.md#comment-une-alerte-naît-se-tait-et-part)),
avec silences et maintenances à part. Le moteur d'alertes lit la page de
statut pour la maintenance.

**Cible** : dès le **premier** geste, un enregistrement par action : qui
(identité de l'opérateur, ou MCP, ou règle), quoi, quand, sur quelle machine,
paramètres sans secret, résultat, journal d'exécution. Stocké dans la base,
**et** envoyé sur un canal « audit » : une base effacée n'efface pas la trace.

Côté alertes, trois notions : **alerte, acquitter, taire**. « Taire »
remplace silence et maintenance : une fenêtre début-fin, sur un objet de
l'annuaire ou un type, jugée à chaque envoi ; elle s'annonce comme maintenance
sur les pages de statut seulement si on le coche. Le moteur d'alertes ne lit
plus les pages. Les canaux restent, globaux par défaut, limitables à des
projets ; leurs essais s'espacent jusqu'à une heure. Le détail est dans le
[tri](#décisions-du-tri-des-fonctionnalités).

Modèles : Komodo (`Update` : opération, opérateur, cible, succès, logs),
Teleport (un événement JSON par ligne, fichier qui « never deletes », export HTTP
générique).

**Coût** : faible pour le journal, à condition de le faire avant le thème 3,
pas après. Moyen pour « taire » : une migration des silences et des
maintenances.

## 8. Quand le serveur tombe

**Aujourd'hui** : rien ne s'arrête (openCloud n'agit pas) ; l'interface, les
alertes et la page de statut sont mortes ; les agents reviennent seuls avec un
délai croissant ; les mesures de la coupure sont perdues.

**Cible et positions**

| Question | Position | Pourquoi |
| --- | --- | --- |
| Les services continuent ? | **Oui, toujours** : Docker et systemd les tiennent ; l'agent garde sa part de l'annuaire et continue de l'appliquer | Nomad et Swarm : « les workloads continuent sans quorum » ; Portainer Edge retente une stack une semaine |
| Les agents se parlent entre eux ? | **Non.** Agent → serveur seulement. Un agent relais pour un site distant : possible plus tard, pas construit | Un maillage multiplie la complexité et la surface d'attaque ; Netdata et Centreon ne le font pas |
| L'interface pendant la panne ? | **Morte, et c'est accepté** | Pour un seul opérateur, un serveur de secours coûte plus qu'il ne rapporte |
| Ce qu'on garantit à la place | **la remise en route rapide** : services vivants, 24 h de données chez chaque agent, base sauvegardée ailleurs et chiffrée ; restaurer = la sauvegarde + la phrase de passe, puis ré-enrôlement si besoin. L'export sert à relire et versionner, pas à restaurer | — |
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
| Une pile par machine, regroupées : un service par machine, regroupés par un projet | Komodo |

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
| préalable | #153 | réglages en base (`settings.toml` disparaît) ; nom du produit à un seul endroit ; secrets de l'interface chiffrés en base. **Fait** : #155, #156, #157 | non |
| 1 | #153 | la direction dans la doc ; l'annuaire : services déclarés et unités, projets, nature des services ; édition sur place des sondes et des tâches ; `export` / `apply` ; vue « écart » | non |
| 2 | #146 | droits côté agent (décisions 2, 9, 10 prises avant) ; vérification réciproque ; rotation | non |
| 3 | à ouvrir | journal d'actions + canal « audit » ; sauvegarde chiffrée de la base vers une destination | oui : la sauvegarde chiffrée, chez l'agent de destination |
| 4 | à ouvrir | rétention locale de l'agent et rejeu | non |
| 5 | à ouvrir | **premier geste** : mettre à jour un agent depuis le serveur | oui |
| 6 | #125 | appliquer une mise à jour d'image validée | oui |
| 7 | à ouvrir | déployer un compose dans `/srv/workspace/<service>/` | oui |
| 8 | #152 | clés SSH distribuées | oui |
| 9 | à ouvrir | modules et dépendances ; catalogue | — |

Rien ne passe à l'étape 5 tant que 2 et 3 ne sont pas fermées. Les contrôles
NRPE et les webhooks entrants viennent après l'étape 2, sans étape fixée.

## Décisions

Prises par Lucas le 17 octobre 2026, une par une. « Mon avis » est celui
donné avant la décision.

| N° | Question | Options | Mon avis | Décision |
| --- | --- | --- | --- | --- |
| 1 | Un seul fichier de réglages ? | garder `settings.toml` + `lifecycle.json` ; tout ramener dans `config.toml` + base | tout ramener | `config.toml` = posé par l'admin avant le démarrage, lecture seule pour le serveur. Ce que l'interface change va en base ; `settings.toml` disparaît. `lifecycle.json` reste un cache, hors export |
| 2 | L'agent reste root ? | root pour tout ; utilisateur `opencloud` + root ciblé | utilisateur dédié, root ciblé, si le coût reste raisonnable | Reportée avant l'étape 2. Piste : observateur non privilégié + exécuteur root étroit avec politique locale |
| 3 | Édition des sondes avant tout ? | oui ; non, on recrée | oui | Oui : un `apply` modifie sur place, ne recrée jamais ; sondes, tâches, composants gardent historique et jeton |
| 4 | Format du fichier déclaratif | YAML ; TOML | YAML, plus courant chez ceux qui écrivent des compose | YAML |
| 5 | `apply` supprime ce qui n'est pas déclaré ? | jamais ; option explicite ; toujours | option explicite, jamais par défaut | Sur option explicite seulement, en affichant ce que la suppression emporte |
| 6 | Version `latest` autorisée au déploiement ? | jamais ; avec avertissement | jamais | Jamais écrite : « dernière version » écrit le numéro du jour, épinglé. openCloud propose les montées, ne les applique jamais seul |
| 7 | Volumes | bind mounts ; volumes nommés ; au choix | bind mounts seulement | Bind mounts seulement, un dossier par service : `compose.yml`, `.env`, conf ; `data/` = données. Sauvegarde d'un service : `data/` + `.env`, chiffrés |
| 8 | Docker seulement pour déployer ? | oui ; aussi des binaires | oui | Oui, dans un premier temps ; les binaires restent supervisés |
| 9 | Liste de droits de départ | la liste du thème 5 ; autre | la liste du thème 5 | **Prise le 21 octobre 2026** : `observe` (implicite, toujours), `agent.update`, `docker.deploy`, `docker.update`, `backup.receive` (un chemin), `files.write` (des chemins). `ssh.keys` attend #152. Stockés et affichés (migration `030_agents.sql`), appliqués avec #146 |
| 10 | Droits dans le fichier de l'agent ou dans l'interface ? | fichier ; interface ; les deux | fichier, l'interface ne fait que les lire | Cochés dans l'interface à l'installation, écrits dans le fichier de l'agent. Qui les change ensuite : reportée avant l'étape 2. Piste : un changement signé par une clé de l'opérateur, pas du serveur |
| 11 | Rétention locale de l'agent | SQLite 24 h ; tampon mémoire ; rien | SQLite 24 h | SQLite 24 h, rejeu à la reconnexion |
| 12 | Sauvegarde de la base | copie locale ; copie + poussée vers un agent ; rien | copie locale d'abord, poussée ensuite | Pas de copie locale : envoi vers une destination choisie (machine avec agent, plus tard stockage externe). Sans destination, l'interface le dit |
| 12 bis | Que contient la sauvegarde, comment se protège-t-elle ? | — | — | Elle contient la clé du serveur et est chiffrée par une paire de clés ; seule une phrase de passe est à garder de côté ([points techniques](#points-techniques-tranchés)) |
| 13 | Jetons de ping | rester en clair ; hacher et ne plus réafficher | à voir, bas dans la liste | Réaffichés en clair dans l'interface, stockés chiffrés en base, régénérables |
| 14 | Premier geste | mise à jour d'un agent ; mise à jour d'image ; déploiement | mise à jour d'un agent | Mise à jour d'un agent, proposée ; à terme tout se met à jour depuis openCloud |
| 15 | Agents entre eux | jamais ; plus tard | jamais | Jamais ; chaque agent garde sa part de l'annuaire, rien de plus. Agent relais pour un site distant : possible plus tard, pas construit |
| 16 | Serveur de secours | non ; plus tard ; maintenant | non, l'architecture ne l'interdit pas | Non ; l'architecture ne l'interdit pas |
| 17 | Icônes | OCI + générique + dépôt ; bibliothèque embarquée ; logos récupérés | OCI + générique + dépôt, en dernier | OCI + générique + dépôt, en dernier |

### Autres décisions actées

| Sujet | Décision |
| --- | --- |
| Installation | L'agent s'installe par une ligne générée par l'interface (`curl … \| sh`), aussi en cloud-init ; jeton à usage unique lu sur stdin ou dans un fichier, jamais en argv. Le serveur n'a jamais d'accès SSH |
| Racine | Racine des services par défaut `/srv/workspace`, réglable dans le fichier de l'agent à l'installation. Dossier existant et non vide : l'installation s'arrête et demande un autre chemin. Fichier témoin `.opencloud` |
| Droits | Pas de « modes » : un seul agent, une liste de droits cochée dans l'interface à l'installation, écrite par la ligne générée dans le fichier local de l'agent. L'agent fait ce qu'il a le droit de faire, rien d'autre |
| Nom | Le nom du produit n'est pas définitif : tout ce qui le porte passe par un seul endroit. Affichage : une constante + les catalogues ; technique : un paquet de constantes (chemins, labels, préfixe de jeton, en-têtes) |
| Identité de service | Le service déclaré a sa propre identité stable ; les conteneurs s'y rattachent : label posé par openCloud pour ce qu'il déploie, sinon machine + projet + service Compose, sinon nom du conteneur (signalé comme faible). Recréer un conteneur ne casse aucun lien |
| Statut | La page de statut globale disparaît : plusieurs pages, une par projet, créées à la main, avec le choix de ce qui est montré |
| Contrôles | Pour après l'étape 2 : contrôles sur le modèle NRPE (commandes dans le fichier local de l'agent, le serveur ne demande qu'un nom, code de sortie 0/1/2/3). Webhooks entrants génériques à côté des pings de cron |

## Décisions du tri des fonctionnalités

Tri de l'existant fait par Lucas le 17 octobre 2026.

| Fonctionnalité | Décision |
| --- | --- |
| Paramètres | Secrets saisis dans l'interface chiffrés en base par la clé du serveur. Paramètres = openCloud lui-même : Général, Accès, Intégrations (MCP, relais e-mail, fin de support). Les seuils vont dans la page Alertes ; la sauvegarde de la base est une sauvegarde parmi d'autres, dans la page Sauvegardes. Machine › Réglages montre ce que l'agent déclare, en lecture |
| Authentification | Compte local créé au premier démarrage, avant tout. SSO branchable ensuite, jamais figé sur un fournisseur : forward-auth (existe) et OIDC dans openCloud (à ajouter). Journal des gestes dès le premier geste ; page de connexion avec déconnexion à la place du Basic ; comptes multiples et rôles plus tard |
| SSO des services | Forward-auth au proxy (générique) + OIDC optionnel par modèle. Par service : tout protégé par défaut, liste de chemins ouverts pré-remplie par le modèle et modifiable, chaque chemin ouvert affiché comme public |
| Tâches | Jeton réaffiché en clair dans l'interface, stocké chiffré en base, régénérable. Intervalle + marge, pas d'horaire cron. Édition sur place, pause qui tient, webhooks entrants comme genre de tâche |
| Sondes | Édition sur place ; rattachement au service de l'annuaire depuis n'importe quelle machine (dedans ou dehors). Contrôles NRPE = un genre de sonde, quatrième état « avertissement » |
| Certificats | Seuils relatifs à la durée de vie : avertissement sous un quart restant, critique sous un dixième ou 2 jours ; réglage global, par sonde plus tard. `ca_file` dans le fichier de l'agent |
| Alertes | Trois notions : alerte, acquitter, taire. « Taire » remplace silence et maintenance : fenêtre début-fin, immédiate ou planifiée, sur un objet de l'annuaire ou un type, jugée à chaque envoi ; case « annoncer comme maintenance sur les pages de statut » décochée par défaut. Limite du canal = protection interne. Plus de CHECK sur les genres |
| Projets | Le projet entre dans l'annuaire : il regroupe des services et sert à leur page de statut, au routage des canaux, au filtre de l'interface, plus tard aux droits. Un service peut appartenir à plusieurs projets : le socle dont dépend un site paraît sur sa page |
| Nature d'un service | **Application** (convertisseur PDF, gestionnaire de mots de passe, site…) ou **outil d'infra** (proxy, SSO, autre outil maison ajouté par Docker). Pré-réglée par le modèle, modifiable. Un outil d'infra peut déclarer ce qu'il fournit (`sso`, `proxy`, `tls`, `backup-target`) : c'est sur quoi les modules se branchent. Séparés dans l'interface et le catalogue ; sur une page de statut, un outil d'infra s'affiche sous un nom générique (« Authentification ») |
| Terme | « Outil d'infra » est provisoire. Le libellé vit dans les catalogues, il se change sans toucher au code |
| Canaux | Globaux par défaut ; limitables à un ou plusieurs projets. Essais espacés jusqu'à une heure, rejoués par la boucle de fond ; code mort retiré |
| Conteneur non couvert | Fiche « non déclaré », adoptable d'un clic dans l'annuaire (vue « écart », migration de l'existant) |
| Images non déployées par openCloud | La commande à copier reste, tant que tout n'est pas migré |
| Réseau | Le dessin interactif est remplacé par une liste par service : publié, dépend de, constats. Le dessin global de l'accueil disparaît |
| Fin de support | Fiche + alerte de faible gravité quand une branche est morte ; produit choisi à la main sur chaque unité (image) du service déclaré ; interrupteur en base |
| Accueil | Devient « ce qui est à traiter » : écarts, alertes, gestes proposés. Les cartes machines vont sur la page Machines |
| Page de statut d'un projet | Composants composés à la main et nommés librement, pré-remplis depuis les services du projet |

## Points techniques tranchés

Tranchés par Claude le 17 octobre 2026, avec l'accord de méthode de Lucas.
Il peut revoir chacun.

| Sujet | Décision | Pourquoi |
| --- | --- | --- |
| Machine openCloud | Reçoit un agent comme les autres ; le chemin intégré à `serve` disparaît | Trois bouts de code recopiés en moins, un seul mécanisme |
| Direct | `machines` publié seulement sur un vrai changement (en ligne, hors ligne, système) ; l'événement porte l'identifiant ; une seule liste de sujets ; un sujet « exécutions » | Aujourd'hui 10 machines = relecture complète toutes les 3 s par onglet |
| Mesures | Gardées ; rétention 24 h chez l'agent (décision 11) ; interfaces virtuelles (veth, ponts) écartées ; agrégat horaire par volume ; historique long par service sur l'identité stable | Trafic compté deux fois, volume plein noyé dans la somme |
| E-mail | Gardé, relais migré en base, mot de passe chiffré | Isolé, facultatif, c'est le canal que tout opérateur a déjà |
| Matrix | Gardé tel quel ; le champ « secret » à double sens sera renommé avec la revue des canaux | 82 lignes, rien à gagner |
| MCP | Lecture seule pendant la refonte ; ensuite les outils passent par la même couche de gestes que l'interface et `apply` ; `Disable` en transaction | Un seul chemin par geste, pas deux à garder alignés |
| Paramètres (front) | `Settings.tsx` découpé en sous-pages | 727 lignes, cinq sujets |
| Service déclaré | Un service = un dossier (projet Compose) avec ses unités (service Compose, conteneur, binaire) ; identifiant jamais tiré de Docker | Colle à la décision 7 : une sauvegarde, une adresse, une version épinglée par image |
| Suppressions | `restrict` entre objets déclarés + un plan qui liste ce que la suppression emporte, puis supprime dans l'ordre ; l'historique propre part avec son objet ; alertes, exécutions et audit survivent (`set null` + nom recopié) | La base refuse toute perte silencieuse, même par un chemin oublié |
| Incidents et « taire » | Visent les objets de l'annuaire ; chaque page de statut montre ceux de ses composants ; le moteur d'alertes ne lit plus les pages | Cohérent avec « taire » et les pages multiples |
| Chiffrement de la sauvegarde | Paire de clés : la publique chiffre chaque nuit sur le serveur ; la privée est chiffrée par la phrase de passe et déposée avec les sauvegardes. Seule la phrase est à garder | Sauvegarde automatique sans secret en clair sur le serveur, une seule chose à retenir |
| Secrets des services | Le `.env` sur la machine fait foi ; openCloud ne le recopie pas en base. La sauvegarde d'un service = `data/` + `.env`, chiffrés | Une seule source de vérité ; une restauration sans les secrets serait inutilisable |
| Export | Ne contient jamais de secrets (marqueur « conservé ») ; peut aller dans Git | Restaurer sur base vide passe par la sauvegarde, pas par l'export |
| Identité dans le YAML | `id` écrit par l'export + `name` ; un `id` dupliqué par copier-coller est refusé | Renommer ne recrée rien (décision 3) |
| Migration de l'existant | Un service déclaré n'est créé d'office que pour un conteneur qui porte déjà un lien (sonde, composant de statut, politique de mise à jour) ; le reste paraît « non déclaré », adoptable d'un clic | Rien ne se crée sans raison, rien ne se perd |
| Unités binaires | Déclarables dès l'étape 1 ; tant qu'aucun code ne les observe, écart « non observé », jamais « conforme » | L'annuaire couvre aussi ce qui n'est pas Docker |
| Conteneur sur `latest` ou sans tag | Écart « version non vérifiable », jamais « conforme » | Cohérent avec la décision 6 |
| Service sur plusieurs machines identiques | Étape 1 : un service par machine, regroupés par un projet (comme une pile par serveur chez Komodo). Les répliques (« 3 attendues, 2 qui tournent ») restent possibles plus tard, non construites. **Décidé par Lucas** | Le plus simple qui couvre le besoin |
| Machine et agent | Deux tables : ré-enrôler, réinstaller ou tourner la clé garde la machine et son historique | Rotation de clé et mise à jour d'agent sans perdre mesures ni liens |

## Propositions du modèle

Ajoutées par le [modèle cible](24-modele-cible.md), tranchées par Lucas le
17 octobre 2026, une par une.

| N° | Proposition | Décision |
| --- | --- | --- |
| 1 | Sauvegardes dans l'annuaire | Oui : destinations et plans de sauvegarde sont dans l'annuaire et le fichier `apply`, secrets exclus. Jamais de sauvegarde sur la machine openCloud. Destination : une machine avec agent (droit « recevoir des sauvegardes »), ou sans agent par SFTP (compte dédié, enfermé dans un dossier, sans shell) ou S3. Pas de rsync. restic est à étudier au chantier Sauvegardes |
| 2 | Unicité du nom d'un service | Unique par machine. Dans le fichier, un service se désigne par machine + nom ; l'interface dit « nginx sur machine X » et accepte un libellé libre. Canaux, sondes et pages visent toujours l'identifiant stable |
| 3 | Services créés sans l'opérateur | Signalés, jamais bloquants. Chaque service porte sa provenance (`declared_by` : opérateur, migration, plus tard découverte automatique) et un drapeau « à vérifier » qui tombe quand l'opérateur valide. Un service à vérifier a ses sondes, alertes et pages comme les autres |
| 4 | Écart de version | Jugé sur le tag ; aussi sur l'empreinte si l'unité l'épingle (`tag@sha256:…`). `latest` ou sans tag : « version non vérifiable » |
| 5 | Historique des conteneurs | 90 jours puis purge, comme les transitions aujourd'hui ; réglable plus tard si le besoin vient |
| 6 | L'actuelle page `/statut` | Devient un projet à la migration, composants et incidents intacts ; `/statut` redirige vers sa page. Plus tard, `/statut` pourra lister les pages publiques |
