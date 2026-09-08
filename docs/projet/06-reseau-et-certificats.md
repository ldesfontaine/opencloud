# Réseau, publication et certificats

## Deux liaisons à ne pas confondre

| | Liaison | Qui parle à qui |
|---|---|---|
| **L1** | **Opérateur → interface** | Un navigateur, depuis n'importe où, vers openCloud |
| **L2** | **Machine openCloud → machines** | openCloud vers les machines qu'il pilote |

Elles n'ont ni les mêmes contraintes, ni les mêmes solutions. Les mélanger est
exactement ce qui rendait la question insoluble.

## L2 — joindre les machines

| Situation | Solution |
|---|---|
| Toutes les machines sur un même réseau privé | **SSH direct.** Rien à monter. |
| Machines avec adresse publique | **SSH direct**, port filtré pour n'accepter que la machine openCloud. |
| Machine sans adresse publique, hors du réseau de la machine openCloud | La machine **sort d'elle-même** par **WireGuard** vers la machine openCloud. Une fois le lien monté, **SSH direct**. |

**Le WireGuard est monté par la commande d'enrôlement**, avec une option
« derrière NAT » : elle crée la clé et monte le lien au démarrage. Un seul
geste (`10-cycle-de-vie.md`).

Pourquoi WireGuard et pas IPsec : c'est du Linux vers Linux. WireGuard est dans
le noyau, une clé par pair, dix lignes de configuration, traverse le NAT sans
négocier. IPsec sert à parler à du matériel réseau ; ici il n'apporterait que de
la négociation et du débogage.

Le VPN ne sert qu'à rendre joignable ce qui ne l'était pas : **un seul chemin
d'exécution**. La machine sans adresse publique est un cas de premier rang.

## L1 — atteindre l'interface

L'interface est servie par le proxy inverse de la machine openCloud, et
**n'écoute jamais sur l'adresse publique sans authentification** (voir
`08-securite-et-secrets.md`).

| Situation de la machine openCloud | Accès |
|---|---|
| Adresse publique | Publiée sur un sous-domaine, derrière authentification. |
| Pas d'adresse publique | **WireGuard depuis le poste de l'opérateur** — le même réseau que ci-dessus —, ou tunnel SSH ponctuel. |

Aucune duplication de l'interface sur plusieurs machines : une seule instance,
un seul point d'accès.

## Le proxy

**Traefik sur chaque machine**, qui route vers les services locaux de cette
machine. Acquis.

Un proxy **frontal** devant tout n'est pas un choix esthétique : c'est la
topologie qui décide.

| Topologie | Proxy frontal |
|---|---|
| Chaque machine exposée a son adresse publique | **Inutile.** Le DNS pointe directement vers la bonne machine. Pas de point unique de défaillance, pas de goulot de bande passante. |
| Une seule adresse publique pour plusieurs machines à exposer | **Obligatoire.** La machine publique reçoit tout et relaie vers les machines privées. |
| Besoin de répartition de charge ou de bascule | **Obligatoire**, et c'est là que HAProxy prendrait le relais — hors périmètre pour l'instant. |

> Direction retenue : **pas de proxy frontal au départ**, DNS direct vers chaque
> machine. Mais concevoir le routage pour qu'ajouter un frontal plus tard ne
> change rien à la configuration Traefik des machines derrière.

## DNS et certificats

- **DNS : Cloudflare, par son API**, en partant du principe que le domaine y est
  délégué. openCloud n'impose pas Cloudflare à l'utilisateur, mais c'est le seul
  fournisseur servi au départ.
- **Challenge ACME : DNS-01.** Il fonctionne partout, y compris sur une machine
  sans adresse publique, et ouvre les certificats génériques
  (`*.exemple.com`). C'est ce qui rend le reste cohérent : une machine privée
  peut obtenir son propre certificat.
- **Chaque machine possède son propre stockage ACME**, et **ce stockage fait
  partie de la sauvegarde** : une restauration le remet en place **avant** de
  relancer Traefik, pour ne pas réémettre et consommer le budget Let's Encrypt
  (voir `07-donnees-et-sauvegardes.md`).
- **Une minuterie légère par machine** s'occupe du renouvellement.
- **Pas de certificat externe** pour l'instant : tout est émis par Let's
  Encrypt.
- **Le TLS termine sur chaque machine.** Pas de frontal qui déchiffre pour les
  autres. **Chaque machine obtient et renouvelle seule ses certificats** :
  Traefik local en DNS-01, avec le jeton de sa zone copié sur la machine
  (tranché le 8 septembre 2026 ; le coût est dit plus bas, « Le jeton DNS »).
- **Deux écritures nommées ailleurs que sur une machine, pas une de plus** : le
  **TXT `_acme-challenge`**, et — **sur demande** — l'**enregistrement A ou
  CNAME** d'un domaine. Tout le reste du DNS est constaté, jamais touché.

### Créer un enregistrement DNS

Une action du catalogue, `A` ou `CNAME`, par l'API Cloudflare. Trois règles qui
ne bougent pas :

| Règle | Ce que ça veut dire |
|---|---|
| **Sur demande** | Jamais automatique, jamais en marge d'une autre action |
| **Toujours montrée** | Nom, type, valeur, zone, affichés avant l'écriture |
| **Proposée au bon moment** | Quand *Créer un hôte virtuel* constate que le domaine ne pointe pas vers cette machine |

Le reste du DNS reste **constaté** : openCloud lit la zone, il ne la range pas
(`15-catalogue-actions.md`).

### La page « certificats »

Une vue dédiée dans l'interface : quel nom est servi par quelle machine, quel
certificat expire quand, lesquels approchent de l'échéance, lesquels ont échoué
au renouvellement. Un bouton **renouveler** pour forcer la main quand il le
faut ; le renouvellement automatique par Traefik reste la voie normale.

C'est aussi la première alerte à câbler : **un renouvellement qui échoue ne se
voit pas** — le certificat reste valide plusieurs semaines, puis le site tombe
d'un coup.

**Trois vérifications avant toute demande et avant tout renouvellement.** Un
refus nommé si l'une échoue, avant le moindre appel à Let's Encrypt :

| Vérification | Ce qu'elle regarde |
|---|---|
| **DNS** | Le nom pointe bien vers cette machine |
| **CAA** | L'enregistrement CAA du domaine n'interdit pas Let's Encrypt |
| **Budget Let's Encrypt** | Tentatives récentes, ce qui reste, l'heure de déblocage |

Le budget est affiché en permanence sur la page. **Le bouton « renouveler »
refuse pendant la fenêtre de blocage** et dit à quelle heure il redeviendra
possible — il ne relance pas dans le vide (`15-catalogue-actions.md` §6).

### Le jeton DNS

Le mécanisme, en trois lignes : pour prouver à Let's Encrypt qu'on possède
`exemple.com`, il faut **écrire une valeur qu'il dicte dans le DNS du domaine**
(`_acme-challenge.exemple.com`), puis lui demander d'aller la vérifier. Écrire
dans le DNS de Cloudflare exige un **jeton d'API**. Ce jeton ne sert qu'à ça :
créer et effacer un enregistrement.

Mais qui peut écrire dans le DNS d'un domaine peut aussi **le détourner** —
rediriger le trafic ailleurs, et se faire délivrer des certificats parfaitement
valides au nom du domaine.

Un **jeton par machine** n'apporterait rien : Cloudflare restreint un jeton à
une zone, pas à un enregistrement — cinq jetons peuvent donc tous tout faire
sur le domaine. Le découpage utile n'est pas la machine, **c'est la zone**.

> **Acté : un jeton par zone Cloudflare.** Il est saisi une fois dans
> openCloud, qui le garde pour l'action *Créer l'enregistrement DNS*, et
> **copié sur chaque machine qui sert la zone** par *Installer le proxy*, dans
> un fichier lisible de Traefik seul. Un jeton qui fuit ne livre que sa zone.

**Pourquoi la copie, et ce qu'elle coûte** (tranché le 8 septembre 2026).
Garder le jeton sur la seule machine openCloud aurait fait dépendre chaque
renouvellement du pilotage — ce que le projet refuse partout ailleurs — et
aurait demandé un client ACME dans le binaire et un dépôt de certificats vers
Traefik. La copie garde la cohérence : Traefik fait tout, sur sa machine, même
la machine openCloud éteinte. Le prix est dit tel quel : **une machine
compromise devient une zone compromise**, et la rotation touche toutes les
machines de la zone.

**La rotation d'un jeton est une action du catalogue.** Un jeton n'est jamais
réaffiché, donc il ne se remplace pas à la main : l'action pose le nouveau sur
chaque machine de la zone, le vérifie, puis retire l'ancien
(`15-catalogue-actions.md`).

**Réduire ce pouvoir reste à l'étude**, avant de certifier (`11-decisions.md`,
partie 2). La piste connue : **déléguer `_acme-challenge` par `CNAME`** vers
une zone dédiée où chaque machine ne peut écrire que son propre
enregistrement ; un jeton compromis ne permettrait alors plus de détourner le
domaine. C'est un composant de plus, à peser le moment venu.

## Ce que le proxy ne publie pas

**Un hôte virtuel, c'est du HTTP.** Un port brut — courrier, base de données,
jeu — n'est pas publié par Traefik et **reste hors du périmètre** : un refus
nommé, et le geste à faire à la main.

Le terrain le demande pourtant (`19-cas-d-usage.md`, §4). **La décision tient**
pour l'instant ; la question est **rouverte au registre**, à reprendre plus
tard (`11-decisions.md`, partie 2).

## Le nom publié, vu du réseau local

Un client du réseau local qui joint son propre service par le nom public sort
par la machine publique et revient : lent, et en panne silencieuse dès que le
lien tombe. La vue Domaines **nomme** la résolution interne attendue. Elle ne la
configure pas.

## Le flux des requêtes et l'adressage — **sujet reporté**

**Aucune décision n'est prise ici.** Ce qui suit décrit le fonctionnement pour
qu'il soit compris avant d'être tranché.

**Sans proxy frontal**, chaque domaine a son enregistrement DNS qui pointe vers
la machine qui le sert :

```
navigateur → DNS (le domaine)
           → adresse IP de la machine qui héberge le site
           → Traefik de cette machine
           → le service
```

Deux sites sur deux machines différentes n'empruntent pas le même chemin et ne
partagent aucune ressource. Le trafic ne transite par aucune machine tierce.

**Avec proxy frontal** (imposé si une seule adresse publique) :

```
navigateur → DNS (tous les domaines pointent vers la même IP)
           → proxy frontal
           → réseau interne / WireGuard
           → Traefik de la machine cible
           → le service
```

Tout le trafic entre par une seule machine : **sa bande passante devient celle
de tout le monde**, et son indisponibilité coupe tous les sites, y compris ceux
hébergés ailleurs.

**Plusieurs adresses sur une même machine**, c'est possible et parfois utile —
séparer un environnement, isoler la réputation d'une adresse. Mais c'est de la
comptabilité en plus : il faut savoir, pour chaque site, quelle adresse le sert.

**Le piège à connaître** : dès qu'un proxy frontal existe, la machine derrière
ne voit plus l'adresse du visiteur, elle voit celle du proxy. Les journaux
d'accès deviennent inutilisables, et **le WAF bannit le proxy** au premier
comportement suspect, coupant tous les sites d'un coup. La correction est
connue — propager l'adresse réelle (`X-Forwarded-For` ou protocole PROXY) et
déclarer le frontal comme intermédiaire de confiance dans Traefik et dans
CrowdSec — mais elle doit être posée **en même temps** que le frontal, pas
après le premier incident.
