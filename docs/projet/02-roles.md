# Les rôles — qui est responsable de quoi

Le principe qui commande tout le reste :

> **La machine openCloud déclenche, observe et rend compte. Elle n'est pas dans
> le chemin critique.** Ce qui doit survivre à son extinction tourne sur la
> machine concernée.

Second principe, pour la joignabilité : *le sens de la connexion réseau varie,
le sens de l'autorité jamais.* Quelle que soit la topologie, openCloud décide
et exécute par SSH (voir `06-reseau-et-certificats.md`).

## La machine openCloud

C'est **une machine ordinaire de l'infrastructure** — ni matériel particulier,
ni rôle réseau spécial. C'est un **rôle**, pas un type de machine : une machine
qui héberge des sites peut le porter en plus.

**Elle est mutualisée.** Sur une infrastructure d'une seule machine, c'est *la*
machine ; ailleurs, elle héberge des services comme les autres. Une machine
dédiée reste possible si l'infrastructure grossit — ce n'est pas le point de
départ.

Quatre choses la distinguent, et quatre seulement :

1. le **programme openCloud** y est installé et sert l'interface web ;
2. elle détient l'**état** — ce qu'openCloud sait de l'infrastructure ;
3. elle détient les **secrets de pilotage** — clé SSH vers les machines, les
   jetons DNS (un par zone), clé de chiffrement des sauvegardes (voir
   `08-securite-et-secrets.md`) ;
4. c'est elle qui **déclenche les actions** et en conserve le résultat.

Ce qu'elle **n'est pas** :

- **pas une passerelle réseau** — le trafic des sites ne passe pas par elle,
  sauf si elle porte aussi le rôle de proxy frontal, qui est un autre rôle ;
- **pas indispensable au fonctionnement** — voir plus bas ;
- **pas l'exécutante** — presque tout s'exécute sur la machine cible ; elle
  n'exécute chez elle que l'enrôlement, les deux écritures DNS, la rotation
  d'un jeton et la récupération des sauvegardes froides (`05-execution.md`).

## Chaque machine gérée

Elle ne décide de rien : elle exécute ce qu'on lui demande, et fait tourner
seule ce qui ne doit dépendre de personne.

| Elle héberge | Elle assure |
|---|---|
| Les services | Leur fonctionnement, indépendamment d'openCloud |
| Traefik (proxy inverse local) | Le routage vers ses services locaux |
| Le stockage ACME | Le renouvellement des certificats, par minuterie locale |
| La sauvegarde locale | Sauvegarde et élagage, par minuterie locale |
| Le collecteur de métriques | Sa propre remontée |
| L'expéditeur de journaux | L'envoi des journaux vers le point de collecte |
| L'agent CrowdSec et le composant de blocage dans Traefik | La lecture des journaux locaux et l'application des décisions |
| Un utilisateur SSH dédié à openCloud | L'exécution des actions envoyées |

### Son statut, quatre états distincts

Un « en ligne » ambigu ne dit rien d'utile. Le statut d'une machine est donc
**honnête** et **séparé** :

| État | Ce qu'il dit exactement |
|---|---|
| **Joignable** | Le dernier test d'accès a réussi |
| **SSH en échec** | La machine répond peut-être ; l'accès d'openCloud, non |
| **Action en cours** | Une action tourne sur elle en ce moment |
| **Dernière remontée** | La date de la dernière information reçue, affichée telle quelle |

L'action *Tester l'accès* tourne **périodiquement, en silence**, et alimente ce
statut (`15-catalogue-actions.md`, `09-observation-et-interface.md`).

## L'opérateur

- **Il déclare** : les machines, les environnements, les services, les domaines.
- **Il décide** : toute action irréversible ou interruptrice passe par sa
  confirmation, et l'interface lui montre ce qui va se produire avant.
- **Il amorce** : il fournit le premier accès à une machine qu'openCloud ne
  joint pas encore.
- **Il garde ce qu'openCloud ne peut pas garder** : la clé de chiffrement des
  sauvegardes, dans son gestionnaire de mots de passe, et le disque physique de
  la copie froide (voir `07-donnees-et-sauvegardes.md`).
- **Il corrige les dérives** : openCloud signale qu'une machine a divergé, il
  ne la répare pas.
- **Il reste responsable de ce qu'il a fait à la main** : openCloud ne le
  reprend pas.

## Les outils tiers déployés

Aucun composant maison ne tourne sur les machines. **Il n'y a pas d'« agent
openCloud »** : il y a un accès SSH et des outils standard, déployés par
openCloud comme il déploierait n'importe quel service. Rien à mettre à jour sur
les machines quand openCloud évolue — c'est l'essentiel du gain.

| Outil | Son rôle | Qui le maintient |
|---|---|---|
| **Traefik** | Router les requêtes vers les services de sa machine, obtenir et renouveler les certificats | Standard, remplaçable |
| **CrowdSec** (agent + composant de blocage) | Lire les journaux locaux, remonter à l'API centrale, appliquer les décisions partout | Standard |
| **Collecteur de métriques** | Raconter l'état de la machine en continu | Standard |
| **Expéditeur de journaux** | Envoyer les journaux au point de collecte | Standard |
| **Minuteries systemd** | Faire tourner sauvegarde, élagage, renouvellement ACME, métriques sans rien demander | Fourni par le système |

Le cas CrowdSec mérite d'être souligné, parce qu'il ressemble à un argument
pour écrire un agent maison alors qu'il n'en est pas un. Un WAF qui lit les
journaux de toutes les machines et décide une fois pour toutes, c'est
**exactement l'architecture native de CrowdSec** : un agent par machine, une
API locale centrale qui reçoit les décisions, des composants de blocage qui les
appliquent partout. Une machine se fait attaquer, toutes les autres bloquent.

> Le besoin d'agent existe, mais il est déjà servi par des outils qui font ça
> mieux qu'un composant maison.

## Les composants centraux

Trois choses ont besoin d'un point central : **l'API du WAF CrowdSec**,
**l'agrégation des métriques** et **la collecte des journaux**.

**Décidé : sur la machine openCloud, avec un plancher de ressources vérifié au
préflight.** Un seul endroit, simple. Elle devient un peu plus critique — elle
l'est déjà.

Le plancher n'est pas un conseil, c'est un refus :

| Situation | Ce qu'openCloud fait |
|---|---|
| La machine a les ressources | Il installe le composant central |
| Elle ne les a pas — ordre de grandeur, **2 Go libres** | Il **refuse** d'installer Loki ou le collecteur central, nomme ce qui manque, et **propose une autre machine** |

Ces composants restent déplaçables sur une autre machine de l'infrastructure
plus tard, sans changer le reste (`15-catalogue-actions.md` §6,
`09-observation-et-interface.md`).

## Quand la machine openCloud est éteinte

C'est l'inquiétude légitime que soulève la centralisation ; elle mérite une
réponse précise.

| Ce qui continue | Ce qui s'arrête |
|---|---|
| Les services et les sites | L'interface web |
| Les sauvegardes locales et l'élagage | Le dépôt de **nouvelles** actions |
| La remontée de métriques, le WAF | La récupération des sauvegardes froides |
| **Les actions déjà lancées** — elles sont détachées de la connexion SSH (voir `05-execution.md`) | |
| Le renouvellement ACME — *sous réserve, voir ci-dessous* | |

**Une exception, assumée** : les jetons DNS — **un par zone Cloudflare** — ne
vivent que sur la machine openCloud, chacun en une seule copie. Le
renouvellement des certificats dépend donc d'elle.

La marge de 30 jours du renouvellement ACME rend l'exception tenable — ici, et
nulle part ailleurs. Le raisonnement complet : `06-reseau-et-certificats.md`.

**La perte de la machine openCloud** ne se répare pas par une bascule
automatique mais par **réinstallation ailleurs**, puis ré-enrôlement des machines
(voir `10-cycle-de-vie.md`).
