# Installer openCloud

Installer openCloud sur un VPS Debian derrière Traefik, ajouter des machines,
le tenir à jour, le sauvegarder. Ce que fait chaque pièce, et pourquoi :
[`docs/projet/22-fonctionnement.md`](projet/22-fonctionnement.md).

> **Ce qui a été vérifié, et où.** Le 10 octobre 2026, ce guide a été
> suivi tel quel sur deux VPS simulés : un système Ubuntu 24.04 minimal
> (debootstrap), amd64, sous **systemd 255** en PID 1, Docker 24. Ont tourné :
> les deux unités systemd, le redémarrage des VPS, **Traefik v3.5.3** en mode
> host puis en conteneur (`host.docker.internal`), `trusted_proxies`,
> l'authentification, les flux en direct, l'enrôlement, une sonde, une
> tâche, l'alerte d'un `docker kill`, le retrait d'une machine, la mise à
> jour, la sauvegarde avec le client `sqlite3` et la restauration.
> **Non testé ici** : Debian 13 lui-même, un certificat Let's Encrypt
> (ACME ; le certificat était auto-signé, ajouté au magasin des machines),
> une machine arm64 réelle.

## Sommaire

1. [Prérequis](#prérequis)
2. [Télécharger](#télécharger)
3. [Compiler](#compiler)
4. [Installer le serveur](#installer-le-serveur)
5. [Configuration](#configuration)
6. [Traefik](#traefik)
7. [Authentification](#authentification)
8. [Envoi d'e-mails](#envoi-de-mails)
9. [Brancher un agent IA](#brancher-un-agent-ia)
10. [Ajouter une machine](#ajouter-une-machine)
11. [Mettre à jour](#mettre-à-jour)
12. [Sauvegarder et restaurer](#sauvegarder-et-restaurer)
13. [Désinstaller](#désinstaller)
14. [Dépannage](#dépannage)

Les fichiers d'exemple sont dans [`docs/installation/`](installation/) :

| Fichier | Où il va |
| --- | --- |
| [`config.toml`](installation/config.toml) | `/etc/opencloud/config.toml`, toutes les clés commentées |
| [`opencloud.service`](installation/opencloud.service) | `/etc/systemd/system/`, le serveur |
| [`opencloud-agent.service`](installation/opencloud-agent.service) | `/etc/systemd/system/` de chaque machine, la machine openCloud comprise : l'agent |
| [`traefik-opencloud.yml`](installation/traefik-opencloud.yml) | Le dossier de configuration dynamique de Traefik |
| [`compose.traefik.yml`](installation/compose.traefik.yml) | Extrait Compose d'un Traefik en conteneur qui joint openCloud sur l'hôte |
| [`compose.mcp-maison.yml`](installation/compose.mcp-maison.yml) | `/srv/workspace/mcp-maison/compose.yml`, mcp-maison pour [brancher un agent IA](#brancher-un-agent-ia) |

## Prérequis

| Quoi | Version | Pour |
| --- | --- | --- |
| Linux | Visé : Debian 13, amd64 ou arm64. Vérifié : Ubuntu 24.04 amd64 | Le serveur et l'agent lisent `/proc` : Linux seulement |
| Go | 1.26.10 ou plus (ligne `toolchain` de `go.mod`) | Compiler, sur le poste de build seulement |
| Node.js | 20.19 ou plus (`web/package.json`) | Compiler l'interface, poste de build seulement |
| `make`, `git` | — | Compiler |
| Docker | API 1.41 ou plus (Docker 20.10+) | Facultatif : suivre les conteneurs d'une machine |
| Traefik | v3 | Facultatif mais conseillé : TLS devant openCloud |
| Un nom de domaine | — | Conseillé : un certificat reconnu, sans `-pin` |
| NTP | — | Les horloges du serveur et des machines à moins de 5 min l'une de l'autre |

Le binaire est statique (`CGO_ENABLED=0`) : rien d'autre à installer sur le
serveur ni sur les machines.

## Télécharger

Les binaires Linux amd64 et arm64 sont dans les
[releases](https://github.com/ldesfontaine/opencloud/releases) du dépôt. Une
version publiée ne change jamais ; elle porte `SHA256SUMS` (les sommes
SHA-256) et `SHA256SUMS.minisig` (leur signature).

**La `v0.0.1` actuelle n'est pas signée** : elle a été réécrite à la main
jusqu'ici. Elle ne se vérifie que par ses sommes :

```sh
base=https://github.com/ldesfontaine/opencloud/releases/download/v0.0.1
curl -fLO "$base/opencloud-linux-amd64" -fLO "$base/SHA256SUMS"
sha256sum -c --ignore-missing SHA256SUMS
mv opencloud-linux-amd64 opencloud && chmod +x opencloud
./opencloud version        # opencloud v0.0.1 (<commit>)
```

La vérification par signature commence à la première version taguée par le
workflow `release` (voir
[`projet/12-methode.md`](projet/12-methode.md#le-jour-où-lucas-décide-dune-version)).
Pour une version signée, `vX.Y.Z` :

```sh
base=https://github.com/ldesfontaine/opencloud/releases/download/vX.Y.Z
curl -fLO "$base/opencloud-linux-amd64" -fLO "$base/SHA256SUMS" -fLO "$base/SHA256SUMS.minisig"
chmod +x opencloud-linux-amd64
./opencloud-linux-amd64 release verify opencloud-linux-amd64
# ok vX.Y.Z opencloud-linux-amd64 sha256:<somme>
mv opencloud-linux-amd64 opencloud
```

`release verify` cherche `SHA256SUMS` et `SHA256SUMS.minisig` à côté du
fichier (options `-sums` et `-sig` pour d'autres chemins), vérifie la
signature avec la clé de version que le binaire embarque, puis la somme du
fichier ; il refuse au moindre écart. Gardez le nom d'origine du fichier
pendant la vérification : la somme se cherche sous ce nom.

Sans faire confiance au binaire qu'on vient de télécharger, on vérifie avec
l'outil `minisign` et la clé publique des versions, puis avec la provenance
GitHub :

```sh
minisign -Vm SHA256SUMS -P <clé publique des versions>
sha256sum -c --ignore-missing SHA256SUMS
gh attestation verify opencloud-linux-amd64 -R ldesfontaine/opencloud
```

La clé publique des versions sera publiée ici le jour de la première version
signée.

## Compiler

Sur un poste avec Go et Node :

```sh
git clone https://github.com/ldesfontaine/opencloud.git
cd opencloud
make build                 # npm ci, compile l'interface, puis bin/opencloud
./bin/opencloud version    # opencloud v0.0.1 (<commit>)
```

Un seul binaire sert de serveur (`serve`) et d'agent (`agent`). Pour une
machine arm64 (Raspberry Pi, Ampere…), la compilation croisée :

```sh
GOARCH=arm64 make build    # bin/opencloud est alors un binaire arm64
mv bin/opencloud bin/opencloud-arm64
make build                 # refaire le binaire amd64
```

`GOARCH=arm64 make build` a été vérifié (ELF aarch64 statique) ; le binaire
n'a pas tourné sur une vraie machine arm64 ici.

Les sous-commandes :

| Commande | Rôle |
| --- | --- |
| `opencloud serve -config <chemin>` | Le serveur web. `-config` vaut `/etc/opencloud/config.toml` par défaut |
| `opencloud agent [options]` | L'agent d'une machine, voir [Ajouter une machine](#ajouter-une-machine) |
| `opencloud auth status\|disable -config <chemin>` | L'état de l'authentification, ou la couper (mot de passe perdu) : l'interface revient à sa première configuration |
| `opencloud secrets forget -config <chemin>` | Clé de chiffrement perdue : repart d'une clé neuve et oublie les secrets qu'elle chiffrait, voir [Clé perdue](#clé-perdue) |
| `opencloud secrets rotate -config <chemin>` | Change la clé de chiffrement et rescelle chaque secret avec la neuve, serveur arrêté, voir [Faire tourner les clés](#faire-tourner-les-clés) |
| `opencloud secrets rotate-server-key -config <chemin>` | Tire une clé neuve pour signer les ordres ; la précédente reste valable le temps que chaque agent reçoive la neuve |
| `opencloud secrets finish-server-key [-force] -config <chemin>` | Retire la clé précédente une fois chaque agent passé à la neuve |
| `opencloud export [-config <chemin>] [-o <fichier>] [-propose]` | Écrit l'annuaire en YAML (sortie standard, ou le fichier en 0600), avec `-propose` y ajoute ce que les conteneurs non déclarés laissent deviner, voir [Exporter l'annuaire](#exporter-lannuaire) |
| `opencloud apply [-config <chemin>] [-prune] [-yes] [-dry-run] <fichier>` | Applique un fichier YAML à l'annuaire, après avoir montré le plan et demandé confirmation, voir [Appliquer un fichier](#appliquer-un-fichier) |
| `opencloud version` | La version et le commit |

## Installer le serveur

Sur le VPS, en root. Un Debian minimal n'a pas `sudo` : les commandes
« sous le compte du service » passent par `runuser`, toujours présent. La
machine openCloud reçoit **deux unités** : le serveur (`opencloud.service`),
et son agent (`opencloud-agent.service`), comme toute machine. Le serveur ne
lit rien de sa machine : sans son agent, elle ne se voit pas.

```sh
# Le binaire, le même pour le serveur et l'agent
install -m 0755 opencloud /usr/local/bin/opencloud

# Un compte système, sans shell. Il n'a besoin ni de root ni du groupe
# docker : c'est l'agent, en root, qui lit Docker.
useradd --system --home-dir /var/lib/opencloud --shell /usr/sbin/nologin opencloud

# La configuration, lisible par le service seulement
install -d -m 0750 -o root -g opencloud /etc/opencloud
install -m 0640 -o root -g opencloud docs/installation/config.toml /etc/opencloud/config.toml
"${EDITOR:-nano}" /etc/opencloud/config.toml   # public_url, listen, trusted_proxies

# Le service
install -m 0644 docs/installation/opencloud.service /etc/systemd/system/opencloud.service
systemctl daemon-reload
systemctl enable --now opencloud
journalctl -u opencloud -f
```

L'unité crée `/var/lib/opencloud` en `0700` (`StateDirectory=`) et lance
`serve` sous `opencloud`. Une installation d'avant mettait le compte dans le
groupe `docker` : `gpasswd -d opencloud docker` le retire, et l'unité n'a
plus de `SupplementaryGroups=docker`.

Au démarrage, le journal dit `listening` avec l'adresse, le répertoire
d'état et la version. Tant qu'aucun compte n'existe, une ligne le rappelle
(`interface awaiting first setup`) : `INFO` si `listen` est sur la boucle
locale, `WARN` sinon, car le premier qui atteint l'adresse crée le compte.
Créez-le tout de suite : [Authentification](#authentification). Tant que la
machine openCloud n'a jamais eu d'agent, une autre ligne dit qu'un jeton
d'enrôlement l'attend (`enrollment token waiting for the agent of this
machine`, le texte dans la langue de l'interface, **jamais le jeton**).

**L'agent de la machine openCloud, dès la première étape.** Le compte créé,
l'écran de première configuration montre la ligne qui l'installe, avec le
droit d'observer seul ; collez-la **sur cette machine**, en root, puis
installez l'unité de l'agent :

```sh
printf '%s\n' 'oc_…' | sudo opencloud agent -server https://opencloud.example.org
# « connected » au journal : Ctrl-C, puis son binaire dans son état
# (v0.0.1 : la version que `opencloud version` affiche)
install -D -m 0755 /usr/local/bin/opencloud /var/lib/opencloud-agent/bin/opencloud-v0.0.1
ln -sfn opencloud-v0.0.1 /var/lib/opencloud-agent/bin/current
install -m 0644 docs/installation/opencloud-agent.service /etc/systemd/system/opencloud-agent.service
systemctl daemon-reload
systemctl enable --now opencloud-agent
```

**L'état de l'agent est hors du dossier du serveur**, sur cette machine comme
sur les autres : `/var/lib/opencloud-agent`, à root en `0700`, alors que
`/var/lib/opencloud` appartient au compte `opencloud`, qui n'a ainsi aucune
prise sur la clé et les droits de l'agent. À son démarrage, l'agent vérifie
que son état et les dossiers au-dessus appartiennent à root (ou à lui) et que
personne d'autre ne peut les modifier ; sinon il le dit au journal (`state dir
is not safe`) et ne garde que `observe` jusqu'à ce que ce soit corrigé. Il se
met à jour depuis sa fiche comme les autres machines ([Mettre à jour les
agents depuis openCloud](#mettre-à-jour-les-agents-depuis-opencloud)). Seule
différence : **pas de lien `/usr/local/bin/opencloud` vers `bin/current`**
ici, car `/usr/local/bin/opencloud` est le binaire du serveur, que rien ne met
à jour seul.

L'agent présente l'empreinte de la machine : c'est celle du serveur, il
devient **la machine openCloud** (identifiant `local`). La ligne vaut 1 h ;
passée, ou pour une installation d'avant les agents (la machine openCloud y
garde son historique, sans agent : elle ne se voit plus), Machines → la
machine openCloud → **Ré-enrôler** en donne une. Sur une installation neuve
où le jeton a expiré, **Ajouter une machine**, jouée sur elle, fait de même.
Le détail : [Ajouter une machine](#ajouter-une-machine).

**Root plutôt qu'un compte dédié ?** Possible : retirez `User=` et `Group=`.
Le compte dédié limite ce que le processus touche ; il ne lit rien de la
machine.

**Les commandes `auth` et `secrets` se lancent sous le compte du service** :

```sh
runuser -u opencloud -- opencloud auth status -config /etc/opencloud/config.toml
```

Lancées en root, elles peuvent créer `opencloud.db-wal` (ou `secret.key`) au
nom de root si le service est arrêté, et `serve` ne pourrait plus écrire dans
sa base. `export` et `apply` font exception : elles n'ouvrent pas la base,
elles parlent au serveur qui tourne par sa socket, et peuvent se lancer en
root (voir [Exporter l'annuaire](#exporter-lannuaire)).

## Configuration

Fichier TOML, par défaut `/etc/opencloud/config.toml`. Le fichier complet et
commenté : [`installation/config.toml`](installation/config.toml).

| Clé | Défaut | Rôle |
| --- | --- | --- |
| `listen` | `127.0.0.1:8080` | Adresse d'écoute, `hôte:port`, en HTTP. Le seul port d'openCloud |
| `state_dir` | `/var/lib/opencloud` | La base `opencloud.db` (et `-wal`, `-shm`), réglages de l'interface compris, et le cache `lifecycle.json`. Créé en `0700` s'il manque |
| `public_url` | vide | L'adresse publique, par exemple `https://opencloud.example.org`. Voir ci-dessous |
| `trusted_proxies` | `[]` | Les mandataires dont les en-têtes `X-Forwarded-*` sont crus, en CIDR |
| `forward_secret_file` | vide | Mode `forward` : le fichier d'une ligne qui porte le secret partagé avec le mandataire (32 caractères au moins, illisible pour les autres comptes). Illisible, trop court ou trop ouvert, il arrête le démarrage. Voir [Derrière un mandataire qui authentifie](#derrière-un-mandataire-qui-authentifie) |
| `log_level` | `info` | `debug`, `info`, `warn` ou `error`. En `debug`, une ligne par requête HTTP |

Une clé inconnue est signalée au démarrage (`unknown key ["…"] ignored`)
puis ignorée ; une valeur invalide arrête le démarrage avec le nom du champ.
`docker_socket` et `ca_file` n'existent plus : le serveur ne lit plus rien de
sa machine, c'est son agent qui le fait (`-docker-socket`, `-ca-file`). Une
configuration d'avant les garde sans dommage, avec cet avertissement.
Le journal est en JSON sur stderr, donc dans `journalctl`.

### `public_url`

Elle sert à deux choses.

1. **Écrire les adresses** : la commande d'installation d'une machine, l'URL
   de ping d'une tâche. Vide, openCloud les déduit de la
   requête (et de `X-Forwarded-Host` / `-Proto` d'un mandataire de
   confiance). Si l'adresse paraît locale, la page du jeton le dit :
   « L'adresse d'openCloud paraît locale : une machine distante ne la
   joindra pas. Renseignez public_url dans la configuration. »
2. **Reconnaître le nom de l'interface.** Toute requête sur `/api/…` doit
   arriver avec un `Host` connu : l'hôte de
   `public_url`, `localhost`, une adresse IP, ou l'hôte de `listen`.
   Derrière un mandataire de confiance, `X-Forwarded-Host` est jugé aussi.
   Sinon : `403 {"error":"bad_host"}`, et le journal dit
   `request refused: unknown Host, set public_url to the address used to reach openCloud`.
   C'est la parade au *DNS rebinding*. Conséquence : **atteindre
   l'interface par un nom de domaine impose de déclarer ce nom dans
   `public_url`**. La page s'affiche, mais l'API refuse.

Un seul nom est reconnu. La page de statut, les pings et les agents ne sont
pas soumis à cette garde.

### `trusted_proxies`

Sans mandataire déclaré, openCloud voit l'adresse de Traefik pour chaque
requête. Ce que ça change :

- l'adresse enregistrée d'une machine est celle de Traefik ;
- la limitation de débit des pings, de l'enrôlement et des échecs
  d'authentification compte **tous les clients comme un seul**, et un
  attaquant peut bloquer l'opérateur ; en IPv6, une source compte pour son
  /64, pas pour son adresse complète ;
- sans `public_url`, les adresses construites ne connaissent pas le nom
  public.

Déclarez l'adresse d'où Traefik se connecte, **seule** : `127.0.0.1/32` en
mode host, son IP fixe en `/32` s'il est en conteneur. Jamais un
sous-réseau : chaque pair de la liste est cru sur parole, et un conteneur
voisin écrirait `X-Forwarded-For` lui-même. Un préfixe plus large qu'une
adresse fait un avertissement au démarrage. `X-Forwarded-For` se lit de la
droite : ce qu'un client écrit lui-même à gauche ne compte pas.

En mode host, `127.0.0.1/32` n'est pas « Traefik seul » : c'est l'adresse
que le noyau donne à **tout processus de la machine**, un autre service, une
application compromise, un conteneur en `network_mode: host`. Pour
`X-Forwarded-*`, un processus local choisit l'adresse que voient le journal
et les limiteurs. Pour l'identité du mode `forward`, ça ouvre tout : voir
[`forward_secret_file`](#derrière-un-mandataire-qui-authentifie).

## Traefik

openCloud écoute en HTTP ; Traefik termine TLS devant lui. Un seul routeur
suffit : interface, API, agents, pings et statut passent par le même
nom et le même port. Vérifié avec Traefik v3.5.3, en mode host et en
conteneur : interface, API, agent, pings, statut et flux en direct passent,
et openCloud voit la vraie adresse des clients. Un essai antérieur avec
`-pin` est noté dans
[22-fonctionnement.md](projet/22-fonctionnement.md#vérifié-le-13-septembre-2026).

openCloud tourne sur l'hôte, pas dans un conteneur : les **labels Docker ne
s'appliquent pas** à lui. On le déclare dans la configuration dynamique de
Traefik (provider `file`) :
[`installation/traefik-opencloud.yml`](installation/traefik-opencloud.yml).

```yaml
http:
  routers:
    opencloud:
      rule: "Host(`opencloud.example.org`)"
      entryPoints: ["websecure"]
      service: opencloud
      tls:
        certResolver: letsencrypt
  services:
    opencloud:
      loadBalancer:
        servers:
          - url: "http://host.docker.internal:8080"   # ou http://127.0.0.1:8080 en mode host
        passHostHeader: true
```

### Comment Traefik joint openCloud

| Cas | `listen` | URL du service Traefik | `trusted_proxies` |
| --- | --- | --- | --- |
| Traefik en mode host (`network_mode: host`, ou paquet sur l'hôte) | `127.0.0.1:8080` | `http://127.0.0.1:8080` | `["127.0.0.1/32", "::1/128"]` : cette adresse couvre tous les processus de la machine, pas Traefik seul. Sans mode `forward`, ça suffit ; avec, ajoutez `forward_secret_file` |
| Traefik en conteneur, réseau créé par Compose, IP fixe | `172.17.0.1:8080` | `http://host.docker.internal:8080` | cette IP seule, par exemple `["172.30.0.2/32"]` |
| Traefik en conteneur, réseau `bridge` par défaut | À éviter : l'IP du conteneur change au redémarrage, et seul un sous-réseau la couvrirait | | |

Pour un Traefik en conteneur ([extrait Compose](installation/compose.traefik.yml)) :

- `extra_hosts: ["host.docker.internal:host-gateway"]` fait pointer
  `host.docker.internal` sur l'IP du pont `docker0`, 172.17.0.1 par défaut,
  quel que soit le réseau du conteneur. Vérifié ici : un conteneur sur un
  réseau `172.18.0.0/16` joint `serve` à `172.17.0.1` par ce nom.
- `listen` doit viser cette IP : `127.0.0.1` n'est pas joignable depuis un
  conteneur. Vérifiez la vôtre avec
  `docker network inspect bridge --format '{{range .IPAM.Config}}{{.Gateway}}{{end}}'`.
- openCloud voit Traefik arriver depuis l'IP de **son** réseau. L'extrait
  la fixe (`ipv4_address: 172.30.0.2` sur un réseau `172.30.0.0/24`) :
  c'est cette adresse seule qui va dans `trusted_proxies`, en `/32`.
  Vérifiez-la avec `docker inspect <conteneur>`.
- `docker0` doit exister quand `serve` démarre : l'unité démarre après
  `docker.service` et redémarre en cas d'échec.
- Écouter sur `172.17.0.1` rend openCloud joignable par **tous** les
  conteneurs de la machine, pas seulement Traefik : l'un d'eux pourrait
  créer le compte avant vous. Faites la [première
  configuration](#authentification) dès le démarrage. Le port n'est pas
  publié sur l'interface publique du VPS, mais un pare-feu qui n'accepte que
  22, 80 et 443 en entrée reste une bonne idée.

### Les flux en direct

`/agent/stream`, `/api/events`, `/api/containers/{id}/logs/stream`,
`/statut/<page>/api/events` sont des flux SSE : une réponse qui dure des
heures. Traefik les laisse passer par défaut. **N'ajoutez pas** de
middleware `buffering` sur ce routeur ; `compress`, s'il est là, ne doit
pas toucher `text/event-stream`. Les délais de Traefik (`readTimeout`,
`idleTimeout`) ne coupent pas un flux : c'est la réponse qui dure, pas la
requête.

### Une authentification Basic au niveau de Traefik

openCloud a sa propre authentification ([plus bas](#authentification)) :
préférez-la. Si vous mettez malgré tout un middleware `basicAuth` devant,
**pas sur ces chemins**, qui ont leur propre preuve ou sont publics :

| Chemin | Qui l'appelle | Sa preuve |
| --- | --- | --- |
| `/agent/…` | L'agent de chaque machine | Sa clé Ed25519 |
| `/ping/…` | Vos tâches planifiées | Le jeton dans l'URL |
| `/statut`, `/statut/…` | Le public : chaque page de statut vit sous ce préfixe, à `/statut/<adresse>` | Aucune, par conception |

Routez le **préfixe** `/statut` entier vers openCloud, pas une page : chaque
page de statut a son adresse dessous (`/statut/client-a`, `/statut/docs`…),
créée dans l'interface sans toucher à Traefik. `/statut` seul mène à la page
s'il n'y en a qu'une, sinon répond 404 : une page ne dit jamais qu'une autre
existe. Une page de statut charge aussi `/assets/…`, `/theme.js` et
`/favicon.svg` : ils doivent rester joignables pour qu'un visiteur la voie. Ne cumulez pas
le Basic de Traefik et celui d'openCloud : un seul en-tête `Authorization`
porte les deux.

## Authentification

**Premier démarrage : ouvrez l'adresse, créez le compte.** Une installation
neuve n'a pas de compte, et ne sert rien d'autre que l'écran de première
configuration : l'interface, l'API, le direct et les journaux
répondent `401` (`auth.setup_required`) tant que le compte n'existe pas. La
page de statut, les pings et les agents gardent leur propre accès ; un jeton
d'API lit l'API sans identifiants (voir [Brancher un agent IA](#brancher-un-agent-ia)). Deux modes : identifiant et mot de passe (HTTP Basic,
ci-dessous), ou [un mandataire qui authentifie](#derrière-un-mandataire-qui-authentifie)
(Authelia). Le compte créé, le navigateur est connecté et l'interface s'ouvre.

**Le premier qui ouvre l'adresse crée le compte.** Créez-le avant de publier
le routeur Traefik, par un tunnel SSH vers l'adresse de `listen` :

```sh
ssh -L 8080:127.0.0.1:8080 root@vps.example.org
# puis, sur votre poste : http://127.0.0.1:8080
```

**Ou par l'API**, depuis la machine, tant qu'aucun compte n'existe :

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"une phrase de 12 caractères ou plus"}' \
  http://127.0.0.1:8080/api/auth/actions/enable
```

| Règle | Valeur |
| --- | --- |
| Mode | HTTP Basic, realm `openCloud`. Le navigateur demande les identifiants dans sa propre fenêtre |
| Identifiant | 1 à 64 caractères, sans `:` |
| Mot de passe | 12 caractères au moins, 72 octets au plus ; haché en bcrypt |
| Échecs | 10 d'un coup, puis un toutes les 6 s par adresse (en IPv6, par /64), `429` au-delà. La vérification lente fait la queue derrière neuf autres au plus : file pleine, `429` aussitôt |
| Effet | Immédiat, sans redémarrage. Créer le compte révoque tous les jetons d'API : il n'y en a qu'après un `auth disable`, recréez ceux qui servent |

Ce qui reste ouvert : la liste du [tableau ci-dessus](#une-authentification-basic-au-niveau-de-traefik).
Le reste — l'interface, `/api/…`, le direct, les journaux — demande les
identifiants, ou un jeton d'API pour les lectures qu'il ouvre. Une fois activée, l'API se
lit avec `curl -u admin:… https://…/api/machines`.

**Pas de déconnexion** : avec Basic, le navigateur garde les identifiants
jusqu'à sa fermeture. HTTP Basic renvoie le mot de passe à chaque requête :
**HTTPS obligatoire** dès que l'interface sort de la machine.

**Mot de passe perdu** :

```sh
runuser -u opencloud -- opencloud auth disable -config /etc/opencloud/config.toml
runuser -u opencloud -- opencloud auth status  -config /etc/opencloud/config.toml
# mode : none — aucun compte, l'interface attend sa première configuration
```

`serve` suit à la requête suivante, sans redémarrer, et l'interface **revient
à sa première configuration** : c'est voulu, la commande locale est la porte
de secours. Rouvrez l'adresse (par le tunnel SSH, pour la même raison qu'au
premier démarrage) et recréez le compte. Désactiver depuis Paramètres fait
de même.

### Derrière un mandataire qui authentifie

Quand un SSO garde déjà l'accès (Authelia en `forwardAuth` de Traefik), le
mode `forward` lui délègue tout : comptes, second facteur, déconnexion.
openCloud laisse passer une requête seulement si elle vient d'une adresse
de `trusted_proxies` **et** porte l'en-tête d'identité non vide
(`Remote-User` par défaut). Tout le reste répond `401`, sans fenêtre Basic :
pas de double connexion.

Une adresse ne prouve pas que la requête vient du mandataire. Un conteneur
voisin sur un réseau Docker, avec Traefik en conteneur et son IP en `/32`,
n'a rien. Mais avec Traefik en mode host, `trusted_proxies` vaut `127.0.0.1/32`,
l'adresse de **tous les processus de la machine** : l'un d'eux qui joint
`127.0.0.1:8080` en direct écrit `Remote-User: x` et devient l'opérateur.
Le secret partagé ferme ce cas, et il est recommandé dans tous les autres.

| Prérequis | Pourquoi |
| --- | --- |
| **Un secret partagé** (`forward_secret_file`), que le mandataire pose dans l'en-tête `X-OpenCloud-Proxy-Secret` | Il prouve que la requête est passée par le mandataire. **Obligatoire** si `trusted_proxies` couvre la boucle locale : openCloud refuse alors d'activer le mode `forward` (`auth.forward_secret_required`). Sans lui, tout processus local est l'opérateur |
| `trusted_proxies` = l'adresse du mandataire **seul**, en `/32` (par exemple `["172.30.0.2/32"]`), pas le sous-réseau | Tout pair de cette liste est cru sur parole : avec un `/24`, un voisin du réseau écrirait l'en-tête lui-même |
| Le mandataire **écrase** l'en-tête envoyé par le client | Sinon un visiteur envoie `Remote-User: admin`. Traefik `forwardAuth` avec `authResponseHeaders: ["Remote-User"]` remplace la valeur du client par celle d'Authelia |
| Deux Traefik en chaîne : `trusted_proxies` vise celui qui joint openCloud, l'en-tête est nettoyé par celui qui appelle Authelia | Le second ne doit être joignable que par le premier : sinon on le contourne, en-tête forgé compris |
| Choisir « Mandataire » sur l'écran de première configuration, ouvert **à travers** le mandataire | Garde-fou : openCloud refuse l'activation (`auth.forward_header_missing`) si la requête ne porte pas déjà l'en-tête, et le secret s'il est configuré, depuis un pair de confiance. On ne peut pas s'enfermer dehors. Derrière Authelia, l'écran lui-même est déjà gardé : pas de course au premier venu |

**Le secret** : créez-le avant d'activer, lisible par le compte du service
et pas par les autres, puis déclarez-le.

```sh
openssl rand -hex 32 > /etc/opencloud/forward.secret
chown root:opencloud /etc/opencloud/forward.secret
chmod 0640 /etc/opencloud/forward.secret
# config.toml : forward_secret_file = "/etc/opencloud/forward.secret"
systemctl restart opencloud
```

Au démarrage, un fichier illisible, de moins de 32 caractères, de plus d'une
ligne ou lisible par les autres comptes (`mode & 0007`) arrête `serve`. Le
secret n'est jamais journalisé. Le contenu du fichier va aussi dans la
configuration de Traefik (ci-dessous) : ce fichier-là doit être aussi peu
lisible. Une requête qui ne porte pas exactement une fois le secret répond
`401`, et l'en-tête est retiré avant les gestionnaires. Changer le secret :
écrire le nouveau dans les deux fichiers, puis redémarrer `serve`.

Les middlewares Traefik : Authelia authentifie, puis le second pose le
secret. `customRequestHeaders` **remplace** la valeur que le client enverrait,
il ne s'ajoute pas. Ajoutez les deux au routeur `opencloud` de
[`traefik-opencloud.yml`](installation/traefik-opencloud.yml) (pas un second
routeur du même nom), **`authelia` d'abord** : `forwardAuth` transmet à
Authelia les en-têtes de la requête, et dans l'autre ordre le secret partirait
avec. Middleware Authelia déclaré par des labels Docker : `authelia@docker`.
Les chemins qui ont leur propre preuve ou sont publics (le tableau
[plus haut](#une-authentification-basic-au-niveau-de-traefik)) ne doivent pas
être gardés par Authelia : une règle `policy: bypass` dans son
`access_control`, sinon les agents, les pings et la page de statut sont
renvoyés vers sa page de connexion.

```yaml
http:
  middlewares:
    authelia:
      forwardAuth:
        address: "http://authelia:9091/api/authz/forward-auth"
        authResponseHeaders: ["Remote-User", "Remote-Groups", "Remote-Email", "Remote-Name"]
    opencloud-proxy-secret:
      headers:
        customRequestHeaders:
          X-OpenCloud-Proxy-Secret: "<le contenu de /etc/opencloud/forward.secret>"
  routers:
    opencloud:
      # … rule, entryPoints, service, tls comme dans traefik-opencloud.yml
      middlewares: ["authelia", "opencloud-proxy-secret"]
```

**Activer** : ouvrez openCloud par son nom public, connectez-vous à
Authelia, puis, sur l'écran de première configuration, Mode « Mandataire
d'authentification ». Une installation qui a déjà un compte Basic y revient
par Paramètres → Accès à l'interface → Désactiver (ou `opencloud auth
disable`). Le corps de l'API est
`{"mode":"forward","header":"Remote-User"}` sur
`POST /api/auth/actions/enable`. Comme en Basic, activer révoque tous les
jetons d'API.

**Installation déjà en `forward` sans secret** : openCloud ne coupe pas
l'accès, mais écrit un `WARN` à chaque démarrage et la page Paramètres →
Accès à l'interface affiche un avertissement tant que `trusted_proxies`
couvre la boucle locale. Posez le secret, redémarrez : l'avertissement
disparaît.

**Vérifier** que le contournement est fermé :

```sh
# depuis la machine openCloud, en direct, en-tête d'identité forgé : refusé
curl -s -o /dev/null -w '%{http_code}\n' -H 'Remote-User: admin' http://127.0.0.1:8080/api/machines
# 401
# de même avec un faux secret
curl -s -o /dev/null -w '%{http_code}\n' -H 'Remote-User: admin' -H 'X-OpenCloud-Proxy-Secret: faux' http://127.0.0.1:8080/api/machines
# 401
# depuis un autre conteneur du même réseau, en-tête forgé : refusé
docker run --rm --network <réseau> curlimages/curl -s -o /dev/null -w '%{http_code}\n' \
  -H 'Remote-User: admin' http://172.30.0.1:8080/api/machines
# 401
# par le nom public, sans session Authelia, en-tête forgé : redirigé vers Authelia, jamais 200
curl -s -o /dev/null -w '%{http_code}\n' -H 'Remote-User: admin' https://opencloud.example.org/api/machines
```

**Mandataire en panne ou mal réglé** : la même commande de secours rouvre
l'accès.

```sh
runuser -u opencloud -- opencloud auth disable -config /etc/opencloud/config.toml
runuser -u opencloud -- opencloud auth status  -config /etc/opencloud/config.toml
```

La contrepartie : tout repose sur `trusted_proxies` juste, sur un
mandataire qui nettoie l'en-tête, et sur un secret qui reste secret. Un
mandataire qui laisse passer l'en-tête, ou un `trusted_proxies` trop large,
et n'importe qui devient l'opérateur. Le secret protège de ce qui joint le
port sans passer par le mandataire ; il ne protège pas d'un compte qui lit
le fichier du secret ou la configuration de Traefik.

## Envoi d'e-mails

Le canal d'alerte e-mail passe par **un relais SMTP que vous fournissez**.
openCloud n'est pas un serveur de courrier : il ne signe rien en DKIM, ne
publie aucun SPF et ne gère aucune réputation d'envoi. Qu'un message arrive
en boîte de réception plutôt qu'en indésirables dépend du relais et du
domaine de l'expéditeur, pas d'openCloud.

**Paramètres → Envoi d'e-mails** : serveur, port, chiffrement, identifiant,
mot de passe, adresse d'expéditeur, puis « Envoyer un e-mail de test » vers
une adresse saisie. Ensuite **Alertes → Canaux → Ajouter un canal**, format
« E-mail », jusqu'à 10 destinataires.

| Chiffrement | Port | Quand |
| --- | --- | --- |
| `starttls` (défaut) | 587 | Le cas courant. Un relais qui ne propose pas STARTTLS est refusé, jamais contourné en clair |
| `tls` | 465 | Le relais attend TLS dès la connexion |
| `none` | 25 ou autre | Seulement un relais sur la boucle locale ou un réseau privé (Postfix local, relais du LAN). L'adresse jointe est vérifiée à chaque envoi |

| Relais | Serveur | Port, chiffrement | Identifiant | Mot de passe | Expéditeur |
| --- | --- | --- | --- | --- | --- |
| Gmail, Google Workspace | `smtp.gmail.com` | 587, `starttls` | l'adresse Gmail | un **mot de passe d'application** (compte Google → Sécurité → validation en deux étapes → mots de passe des applications) ; le mot de passe du compte est refusé | l'adresse Gmail elle-même |
| Brevo | `smtp-relay.brevo.com` | 587, `starttls` | l'identifiant SMTP affiché dans Brevo → SMTP & API | la clé SMTP générée au même endroit | une adresse d'un domaine ou d'un expéditeur validé dans Brevo |
| Le relais de l'hébergeur | celui qu'il indique | celui qu'il indique | souvent la boîte du domaine | celui de la boîte | une adresse de la boîte |
| Postfix sur la machine | `127.0.0.1` | 25, `none` | vide | vide | une adresse du domaine que Postfix relaie |

Les quotas d'envoi sont ceux du relais : Gmail limite le nombre de messages
par jour, Brevo selon l'offre. Un refus du relais se lit dans l'historique
des envois de l'alerte : « le relais refuse l'identifiant ou le mot de passe »
(code SMTP 535 en général), « le relais refuse le message ou un
destinataire », « chiffrement refusé avec le relais ».

Le relais vit dans la base, **mot de passe compris, chiffré** avec les
autres secrets saisis dans l'interface (voir
[Sauvegarder et restaurer](#sauvegarder-et-restaurer)) : `opencloud.db` est
sous `state_dir`, en `0700` ; l'interface et l'API ne le réaffichent jamais.

## Brancher un agent IA

openCloud ne parle pas MCP lui-même. **mcp-maison**, un serveur MCP à part,
tourne sur la machine openCloud dans Docker, lit l'API avec un **jeton d'API
en lecture**, et sert ses outils à Claude (claude.ai, Claude Code) ou à tout
client MCP. Chaque personne s'y connecte par l'IdP OIDC de l'opérateur
(Authelia, Authentik, Keycloak, GitLab). openCloud marche sans lui.

| Il faut | Pourquoi |
| --- | --- |
| Docker Compose 2.24 ou plus sur la machine openCloud | mcp-maison est une image épinglée |
| Un IdP OIDC | La seule identité que mcp-maison accepte en production |
| Un nom, `mcp.example.org`, routé par Traefik vers mcp-maison | claude.ai le joint en HTTPS |
| Le port 3000 libre sur l'hôte de `listen` | mcp-maison écoute à côté d'openCloud |

**1. Le jeton.** Paramètres → **Jetons d'API** → nom « mcp-maison », échéance →
**Créer**. Trois lignes s'affichent une fois : `MCP_HOST`, `OPENCLOUD_URL`
(tirés de `listen` : mcp-maison joint openCloud en direct, jamais par Traefik
ni le SSO, qui arrêterait le jeton) et `OPENCLOUD_TOKEN`.

**2. L'application chez l'IdP.** Retour `https://mcp.example.org/oauth/callback`,
client confidentiel, scopes `openid profile email`. Avec Authelia
(`identity_providers.oidc.clients`) :

```yaml
- client_id: 'mcp-maison'
  client_name: 'mcp-maison'
  client_secret: '<empreinte : authelia crypto hash generate pbkdf2>'
  public: false
  authorization_policy: 'two_factor'
  consent_mode: 'explicit'
  redirect_uris: ['https://mcp.example.org/oauth/callback']
  scopes: ['openid', 'profile', 'email']
  token_endpoint_auth_method: 'client_secret_post'
```

`client_secret_post` : mcp-maison envoie le secret dans le corps.

**3. Les fichiers**, en root :

```sh
install -d -m 0750 /srv/workspace/mcp-maison
cd /srv/workspace/mcp-maison
install -m 0644 <dépôt>/docs/installation/compose.mcp-maison.yml compose.yml
install -d -o 1000 -g 1000 -m 0750 data/mcp
install -m 0600 /dev/null .env && "${EDITOR:-nano}" .env
install -o 1000 -g 1000 -m 0640 /dev/null data/mcp/permissions.yaml
"${EDITOR:-nano}" data/mcp/permissions.yaml
```

Le `.env` :

```sh
# Les trois lignes d'openCloud (Paramètres → Jetons d'API), montrées une fois
MCP_HOST=127.0.0.1
OPENCLOUD_URL=http://127.0.0.1:8080
OPENCLOUD_TOKEN=
# mcp-maison derrière Traefik, sous son propre nom
MCP_PUBLIC_URL=https://mcp.example.org
ALLOWED_HOSTS=mcp.example.org
TRUST_PROXY=1
# L'application déclarée chez l'IdP, retour ${MCP_PUBLIC_URL}/oauth/callback
OIDC_ISSUER=https://auth.example.org
OIDC_CLIENT_ID=mcp-maison
OIDC_CLIENT_SECRET=
```

Les droits, `data/mcp/permissions.yaml` (sans ce fichier : `ping` et `whoami`
seulement) :

```yaml
# Droits de mcp-maison (docs/droits.md de mcp-maison).
everyone: [core:ping, core:whoami]
users:
  # L'identifiant chez l'IdP (whoami le montre). Authelia ne laisse pas une
  # personne changer le sien ; ailleurs, préférez groups.
  admin: [opencloud:read, core:audit]
```

**L'image est épinglée.** `compose.mcp-maison.yml` nomme une version de
mcp-maison, `ghcr.io/ldesfontaine/mcp-maison:v0.1.0`, et une ligne de
commentaire demande d'y ajouter son empreinte (`@sha256:…`) dès que la
version est publiée : `docker buildx imagetools inspect
ghcr.io/ldesfontaine/mcp-maison:v0.1.0` la donne. Une version se change en
changeant cette ligne, rien d'autre.

**Tant qu'aucune version n'est publiée** (mcp-maison#3 : aucun tag au
22 octobre 2026), `docker compose up` ne trouve pas l'image ; le paquet doit
aussi être public pour un `docker pull` sans identifiants (mcp-maison#4).
Construisez-la depuis le commit vérifié avec cette version d'openCloud, puis
faites pointer la ligne `image:` dessus. Le dépôt de mcp-maison est privé
aujourd'hui : le clone demande un accès (`gh auth login`, ou un jeton) :

```sh
git clone https://github.com/ldesfontaine/mcp-maison.git /srv/workspace/mcp-maison/src
git -C /srv/workspace/mcp-maison/src checkout 0b55aac
docker build -t mcp-maison:0b55aac /srv/workspace/mcp-maison/src
sed -i 's|^\( *image:\).*|\1 mcp-maison:0b55aac|' compose.yml
```

**4. Lancer, vérifier.**

```sh
docker compose up -d && docker compose ps     # mcp : healthy
curl -s http://127.0.0.1:3000/health           # des outils opencloud_*, skippedGroups vide
```

Dans `/etc/opencloud/config.toml` : `mcp_url = "http://127.0.0.1:3000"` (l'hôte
de `MCP_HOST`), puis `systemctl restart opencloud`. Paramètres → **Agent IA** →
**Vérifier** : « Joignable », la version, le nombre d'outils. Sous **Jetons
d'API**, le jeton montre son dernier usage.

**5. Traefik → mcp-maison**, comme openCloud, par le provider `file`, **sans
middleware SSO** : Claude doit atteindre `/mcp`, `/.well-known/…`,
`/register`, `/authorize`, `/token`, `/revoke`, `/oauth/callback`.

| `listen` d'openCloud | `MCP_HOST` | URL du service Traefik |
| --- | --- | --- |
| `127.0.0.1:8080` (Traefik en mode host) | `127.0.0.1` | `http://127.0.0.1:3000` |
| `172.17.0.1:8080` (Traefik en conteneur) | `172.17.0.1` | `http://host.docker.internal:3000` |
| L'IP privée de la machine (Traefik sur une autre machine) | cette IP | `http://<cette IP>:3000` |

mcp-maison accepte `OPENCLOUD_URL` en `http://` sur la boucle locale
(`localhost`, `127.0.0.1`, `[::1]`) et vers une IP privée RFC 1918 (`10.0.0.0/8`,
`172.16.0.0/12`, dont `172.17.0.1`, `192.168.0.0/16`) : les trois lignes
marchent. Un nom ou une IP publique en `http://` lui fait ignorer son groupe
`opencloud` (`groupe "opencloud" non chargé : … https:// attendu` à son
journal, « Sans ses outils openCloud » sous Paramètres → Agent IA). L'hôte de
`OPENCLOUD_URL` reste jugé par openCloud : une IP, `localhost` ou le nom de
`public_url` ; un nom de conteneur ou `host.docker.internal` reçoivent
`403 {"error":"bad_host"}`.

Le routeur, dans la configuration dynamique de Traefik (comme
[`traefik-opencloud.yml`](installation/traefik-opencloud.yml), sans
`middlewares` ni `buffering` : `/mcp` est un flux) :

```yaml
http:
  routers:
    mcp-maison:
      rule: "Host(`mcp.example.org`)"
      entryPoints: ["websecure"]
      service: mcp-maison
      tls:
        certResolver: letsencrypt
  services:
    mcp-maison:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:3000"   # la colonne de droite ci-dessus
```

**6. Claude.** claude.ai : Paramètres → Connecteurs → ajouter un connecteur
personnalisé, `https://mcp.example.org/mcp`, sans identifiant ni secret.
Claude Code : `claude mcp add --transport http opencloud https://mcp.example.org/mcp`,
puis `/mcp` pour se connecter.

**Ce que l'agent lit.** Machines, services, tâches, sondes, alertes, pages de
statut, journal d'audit, et les **journaux bruts des conteneurs**, qui partent
chez le fournisseur du modèle et peuvent contenir des secrets. Un jeton d'API
ne fait que lire : 25 lectures de l'API, rien d'autre (`403
api_token.route_forbidden`), les secrets d'openCloud masqués (URL de ping,
jetons d'enrôlement, chemin d'un webhook, mot de passe d'une cible de sonde).
Il n'écrit rien et n'agit sur aucune machine. Le détail, les refus et le
journal d'audit :
[22-fonctionnement.md](projet/22-fonctionnement.md#comment-un-agent-ia-lit-opencloud).

## Ajouter une machine

### 1. Le jeton

Dans l'interface : Machines → **Ajouter une machine**, un nom en lettres
minuscules, chiffres et tirets, et les **droits** de l'agent à cocher.
openCloud affiche une seule fois la commande à coller :

```sh
printf '%s\n' 'oc_…' | sudo opencloud agent -server https://opencloud.example.org
```

Les droits : `observe` (toujours, la case est grisée), mettre à jour
l'agent, déployer un service Docker, mettre à jour un service Docker,
recevoir des sauvegardes dans un dossier, écrire des fichiers dans des
dossiers (un à huit chemins absolus, séparés par des virgules). Au-delà de
`observe`, la commande les porte en clair, à lire avant de coller :

```sh
printf '%s\n' 'oc_…' | sudo opencloud agent -server https://opencloud.example.org -rights observe,agent.update,files.write=/srv/workspace
```

Ils s'écrivent dans `identity.json`, la fiche de la machine les montre, et
**l'agent les applique** : un ordre hors de ses droits est refusé, et le refus
se lit dans les ordres de la fiche et au journal. Pour les changer : fiche ›
Agent › **Changer les droits**, qui émet un jeton de ré-enrôlement avec les
nouveaux ; collez sa ligne sur la machine après avoir supprimé
`identity.json` (voir [Ré-enrôler, retirer](#ré-enrôler-retirer)).

Le jeton passe par l'**entrée standard** de l'agent, jamais par sa ligne de
commande : une ligne de commande se lit dans `ps` et `/proc/<pid>/cmdline`
depuis tous les comptes de la machine, et le premier qui copie le jeton
prend l'identité de la machine. `printf` est une commande interne du shell,
qui ne crée aucun processus : le jeton n'apparaît dans les arguments
d'aucun. Si `sudo` demande un mot de passe, il le lit sur le terminal
(`/dev/tty`), pas sur le tube : la commande se colle telle quelle. Collez-la
dans une session ouverte sur la machine, pas en argument de
`ssh machine '…'` : la commande entière, jeton compris, deviendrait les
arguments du shell distant (et sans terminal, `sudo` ne pourrait pas
demander son mot de passe).

Sur la machine openCloud elle-même, la commande installe **son** agent :
l'empreinte de la machine le rattache à la machine openCloud. Si elle a déjà
un agent actif, l'enrôlement est refusé (« cette machine est la machine
openCloud, et elle a déjà un agent actif ») et le jeton reste valable pour
une autre machine : pour remplacer son agent, Ré-enrôler sur sa fiche.

Connecté en root sans `sudo` (Debian minimal) : collez la commande sans
`sudo`. À la main, sur un terminal, `sudo opencloud agent -server https://…`
seul demande le jeton (« Jeton d'enrôlement : ») et le lit sur la ligne
suivante ; il s'affiche à la saisie.

Le jeton sert **une fois** et expire en **1 h**. L'adresse vient de
`public_url` : vérifiez-la avant de coller. `-lang en` s'ajoute si
l'interface est en anglais.

### 2. Le binaire

```sh
scp bin/opencloud root@machine:/tmp/opencloud          # bin/opencloud-arm64 pour arm64
ssh root@machine 'install -d -m 0700 /var/lib/opencloud-agent \
  && install -D -m 0755 /tmp/opencloud /var/lib/opencloud-agent/bin/opencloud-v0.0.1 \
  && ln -sfn opencloud-v0.0.1 /var/lib/opencloud-agent/bin/current \
  && ln -sfn /var/lib/opencloud-agent/bin/current /usr/local/bin/opencloud'
```

Remplacez `v0.0.1` par la version du binaire (`/tmp/opencloud version`). Le
binaire vit dans `bin/` de l'état de l'agent et le lien `current` désigne
celui qui tourne : c'est ce qui permet à l'agent de se mettre à jour seul
(voir [Mettre à jour](#mettre-à-jour)). `install -d -m 0700` crée l'état en
`0700`, à root, comme l'agent le ferait (`install -D` seul crée ses dossiers
en `0755`). `/usr/local/bin/opencloud` devient un lien vers
`bin/current` : la commande `opencloud` suit les mises à jour. **Pas ce lien
sur la machine openCloud** : `/usr/local/bin/opencloud` y est le binaire du
serveur (voir [Installer le serveur](#installer-le-serveur)).

### 3. Le premier lancement : l'enrôlement

Sur la machine, en root, la commande affichée :

```sh
printf '%s\n' 'oc_…' | sudo opencloud agent -server https://opencloud.example.org
```

L'agent tire une clé Ed25519, s'enrôle, écrit
`/var/lib/opencloud-agent/identity.json` en `0600`, puis se connecte. La
réponse de l'enrôlement porte la **clé publique d'openCloud** : l'agent
l'épingle dans `identity.json` et n'exécutera que les ordres qu'elle signe.
Le journal la nomme (`"server_key":"SHA256:…"`) : c'est l'empreinte que
`GET /api/agents/server-key` rend, et que la fiche de la machine compare. Quand le
journal dit `"msg":"connected"`, arrêtez-le (Ctrl-C) : le service prend le
relais.

| Option | Retenue dans l'identité | Rôle |
| --- | --- | --- |
| `-server` | Oui | Adresse d'openCloud, premier lancement seulement |
| `-pin` | Oui | Empreinte SHA-256 du certificat d'openCloud, s'il est auto-signé |
| `-lang` | Oui | Langue des messages de l'agent, `fr` ou `en` |
| `-rights` | Oui | Droits cochés dans l'interface ; sans lui, `observe` seul. Un droit mal écrit est refusé avant l'enrôlement (code 2), le jeton reste valable |
| `-root-dir` | Oui | Racine des services que l'agent déploie (`docker.deploy`, `docker.update`), `/srv/workspace` par défaut, premier lancement seulement. Un chemin absolu et propre. Chaque service s'y écrit dans `<racine>/<projet>/` |
| `-state` | Non | Répertoire d'état, `/var/lib/opencloud-agent` par défaut |
| `-allow-plain` | **Non** | Accepte `http://` vers un serveur distant |
| `-docker-socket` | **Non** | Socket Docker, `/var/run/docker.sock` par défaut |
| `-ca-file` | **Non** | Autorité interne pour les sondes de cette machine |

Les options marquées **Non** se redonnent à chaque lancement : mettez-les
dans l'unité systemd.

### 4. Le service

```sh
install -m 0644 opencloud-agent.service /etc/systemd/system/opencloud-agent.service
systemctl daemon-reload
systemctl enable --now opencloud-agent
journalctl -u opencloud-agent -f
```

L'unité ([`opencloud-agent.service`](installation/opencloud-agent.service))
lance `/var/lib/opencloud-agent/bin/current agent -state
/var/lib/opencloud-agent`, sans `-server` ni jeton : l'agent relit son
identité (`identity loaded`) et se connecte. Il tourne en root : il lit
chaque volume monté et la socket Docker.

**Un droit qui écrit ailleurs que dans l'état** (« Recevoir des sauvegardes »,
« Déployer » et « Mettre à jour un service Docker ») demande un fichier de
plus : l'unité n'écrit que dans son état (`ProtectSystem=strict`), le dossier
du droit doit être autorisé. La page du jeton donne le fichier ; par exemple
pour `/srv/backups` :

```sh
install -d -m 0700 /srv/backups
mkdir -p /etc/systemd/system/opencloud-agent.service.d
printf '[Service]\nReadWritePaths=/srv/backups\n' > /etc/systemd/system/opencloud-agent.service.d/droits.conf
systemctl daemon-reload
systemctl restart opencloud-agent
```

Le dossier doit exister avant le redémarrage : systemd refuse de démarrer
une unité dont un `ReadWritePaths=` manque. Sans ce fichier, l'agent accepte
l'ordre, ne peut pas écrire, et répond `path_unwritable`.

**Le code de sortie 3** veut dire « mis à jour, relance-moi » : l'agent vient
de changer de binaire (`agent.update`) et s'arrête pour que systemd le relance
sur le nouveau. `Restart=on-failure` le relance, `RestartPreventExitStatus=2`
ne retient que le code 2.

**Déployer un service Docker** (droits `docker.deploy` et `docker.update`)
demande deux choses sur la machine, et une dans l'unité :

- **Docker et Docker Compose 2.24 au moins** (`docker compose version --short`),
  l'exécutable à `/usr/bin/docker`. Sans eux, l'ordre est refusé
  (`docker_missing`, `compose_too_old`) ; il l'est aussi sous 2 Gio libres sous
  la racine (`disk_full`).
- **La racine des services** (`/srv/workspace` par défaut, ou celle de
  `-root-dir`) : créée par l'agent si elle manque, mais l'unité doit
  l'autoriser, et systemd refuse de démarrer sur un chemin absent, donc créez-la
  d'abord. Si elle existe, elle doit être vide ou porter le fichier témoin
  `.opencloud` (`root_not_ours` sinon : l'agent n'écrit pas dans un dossier qui
  n'est pas à lui).

```sh
install -d -m 0755 /srv/workspace
mkdir -p /etc/systemd/system/opencloud-agent.service.d
printf '[Service]\nReadWritePaths=/srv/workspace\n' > /etc/systemd/system/opencloud-agent.service.d/droits.conf
systemctl daemon-reload
systemctl restart opencloud-agent
```

Avec la sauvegarde, une seule ligne `ReadWritePaths=` liste les deux dossiers,
séparés par une espace. Le `compose.yml` d'un service déployé est **réécrit à
chaque déploiement** : ne le retouchez pas à la main. Les dossiers `data/<unité>`
sont créés en `0755`, root : si le conteneur tourne sous un autre utilisateur,
donnez-lui le dossier (`chown`) avant le premier lancement.

Une coupure de réseau ou un redémarrage de `serve` ne l'arrête pas : il se
reconnecte seul, avec un délai de 1 à 60 s, et signe à chaque fois une
preuve neuve (un aléa qu'il tire, l'heure) : il n'a rien à demander au
serveur avant. Il ne suit aucune redirection : un proxy qui répond par une
3xx sur `/agent/…` le laisse hors ligne, il faut que `-server` vise
l'adresse finale. Il s'arrête, code de sortie 2, quand openCloud refuse son identité (machine
retirée, signature qui n'est plus celle de la clé enrôlée, `bad_proof`, ou
agent révoqué, `revoked`), ou quand il ne parle pas la version du
protocole du serveur (`protocol_unsupported`, voir [Mettre à jour](#mettre-à-jour)) :
l'unité ne le relance pas (`RestartPreventExitStatus=2`) et `systemctl
status` la montre `failed`, code 2. C'est voulu : il faut un nouveau jeton.

### Sans domaine : `-pin`

Un openCloud joint par IP, avec un certificat auto-signé, n'est pas
reconnu par le magasin du système. `-pin` épingle **ce** certificat :
l'agent n'accepte que lui, sans vérifier la chaîne.

```sh
# L'empreinte, lue depuis la machine openCloud (le fichier du certificat) :
openssl x509 -in cert.pem -noout -fingerprint -sha256 | cut -d= -f2 | tr -d :
# ou à distance, en vérifiant ensuite qu'elle correspond :
openssl s_client -connect 203.0.113.10:443 </dev/null 2>/dev/null \
  | openssl x509 -outform DER | sha256sum | cut -d' ' -f1

printf '%s\n' 'oc_…' | sudo opencloud agent -server https://203.0.113.10 -pin <empreinte>
```

L'empreinte s'écrit en hexadécimal, majuscules ou minuscules, préfixe
`sha256:` facultatif, **sans les deux-points**. Le certificat change :
l'agent refuse, il faut ré-enrôler avec la nouvelle empreinte.

### Sans TLS : `-allow-plain`

Vers une adresse qui n'est pas la boucle locale, l'agent refuse `http://` :
le jeton passerait en clair. `-allow-plain` lève ce refus. **Seulement sur
un réseau déjà chiffré** (WireGuard, Tailscale). Il n'est pas retenu :
ajoutez-le aussi à `ExecStart=` de l'unité.

### Ré-enrôler, retirer

| Besoin | Faire |
| --- | --- |
| Changer la clé d'une machine (identité perdue, certificat épinglé changé) | Machines → la machine → **Ré-enrôler** : un nouveau jeton, avec les droits que demandait celui de l'agent en place. Sur la machine : arrêter le service, **supprimer `identity.json`**, relancer la commande d'installation (jeton sur l'entrée standard). Un nouvel agent est inscrit, l'ancien révoqué ; l'identifiant de la machine et l'historique sont gardés |
| Changer les droits de l'agent | Machines → la machine → Agent → **Changer les droits** : les droits du jeton courant sont cochés, changez-les, puis **Créer le jeton**. La suite est celle d'un ré-enrôlement (arrêter le service, supprimer `identity.json`, coller la ligne, qui porte `-rights`). Modifier `rights` dans `identity.json` à la main marche aussi, l'agent applique son fichier, mais la fiche montre alors un écart avec le jeton |
| Couper un agent sans perdre la machine (machine compromise, clé volée) | Machines → la machine → **Révoquer l'agent**. Son flux tombe, sa prochaine preuve est refusée (`revoked`) et il s'arrête, code 2 ; les jetons de ré-enrôlement en attente sont annulés. La machine, son historique, ses services restent ; elle passe hors ligne sans être déclarée perdue. Pour la rétablir : **Ré-enrôler**, sur une machine saine. Ce que l'agent détenait (les `.env` des services de la machine) reste à changer à la main |
| Retirer une machine | Machines → la machine → **Retirer**. La confirmation montre d'abord ce que le retrait emporte (mesures, conteneurs…) et ce qui le retient : services déclarés, sondes qu'elle lance, tâches rattachées, composants de la page de statut. Tant qu'il en reste, le bouton est fermé ; supprimez-les ou détachez-les (changer la machine d'une sonde, retirer la machine d'une tâche ou d'un composant). Son historique est effacé ; l'agent s'arrête en disant « openCloud ne reconnaît plus cette machine » |

Tant que `identity.json` existe, l'agent l'utilise et ne lit jamais l'entrée
standard : il ne bloque pas sous systemd. Le jeton ne passe jamais en
argument : `-token` est refusé, il serait visible de tous les comptes via `ps`.

## Mettre à jour

Il n'y a ni paquet ni dépôt : on remplace le binaire, après l'avoir vérifié
(`opencloud release verify`, voir « Télécharger »). Les migrations de la
base s'appliquent **seules au démarrage**, dans l'ordre, chacune dans sa
transaction, notées dans `schema_migrations`. Pas de commande à lancer, pas
de retour arrière : **sauvegardez avant**. Un binaire qui trouve une
migration qu'il ne connaît pas (une base venue d'un binaire plus récent)
refuse de démarrer, sans rien toucher : pour revenir en arrière, remettez
l'ancien binaire **et** la copie `avant-maj.db`.

La v0.0.1 part d'un schéma unique (`migrations/001_schema.sql`) : une base
créée avant lui (les migrations `002` à `034` d'avant le 21 octobre 2026) est
refusée de même. Il n'y a pas de montée : arrêtez `serve`, déplacez
`opencloud.db` (et ses fichiers `-wal`, `-shm`), relancez ; la base repart
vide, et chaque machine se ré-enrôle (supprimer son `identity.json`, relancer
la ligne d'installation).

```sh
# Sur le poste de build
git pull && make build

# Sur le serveur : sauvegarder, puis remplacer et redémarrer
runuser -u opencloud -- sqlite3 /var/lib/opencloud/opencloud.db ".backup '/var/lib/opencloud/avant-maj.db'"
install -m 0755 opencloud /usr/local/bin/opencloud
systemctl restart opencloud

# L'agent a son binaire dans son état (bin/), sur la machine openCloud comme
# ailleurs : depuis la fiche (voir plus bas), ou à la main
umask 077 && install -D -m 0755 opencloud /var/lib/opencloud-agent/bin/opencloud-v0.0.2
ln -sfn opencloud-v0.0.2 /var/lib/opencloud-agent/bin/current
systemctl restart opencloud-agent
```

`install` remplace le binaire pendant que le service tourne (vérifié ici) ;
le nouveau prend effet au redémarrage. `v0.0.2` est la version installée
(`opencloud version`). Sur une machine que l'agent met à jour seul,
n'utilisez pas `install` sur `/usr/local/bin/opencloud` : c'est un lien vers
`bin/current`, `install` le remplacerait par une copie qui ne suivrait plus.
Sur la machine openCloud, `/usr/local/bin/opencloud` est le binaire du serveur
(le premier `install` ci-dessus) : la copie de l'agent est à part, dans `bin/`.

### Mettre à jour les agents depuis openCloud

Une fois le serveur à jour, les autres machines se mettent à jour **depuis la
fiche**, sans y entrer. Le serveur n'envoie que **sa propre version**, signée :

```sh
# Sur le serveur : télécharger la version (Télécharger, ci-dessus), la vérifier,
# puis l'importer dans la bibliothèque de versions du serveur
# Le dossier doit être lisible par le compte opencloud (/tmp/v0.0.2, pas /root/…)
opencloud release verify opencloud-linux-amd64          # dans le dossier de la version
runuser -u opencloud -- opencloud release import /tmp/v0.0.2
```

Il faut un binaire qui embarque la clé de version (la première version
signée, pas la `v0.0.1`) : sans elle, la fiche dit que la mise à jour des
agents est coupée (`no_releases`), et chaque machine se met à jour à la main
comme ci-dessus.

`release import <dossier>` lit le dossier d'une version publiée
(`SHA256SUMS`, `SHA256SUMS.minisig` et les binaires `opencloud-linux-*`),
vérifie tout avec la clé embarquée, puis le range sous `releases/` dans l'état
d'openCloud. Il refuse le dossier entier au moindre écart (signature, binaire
manquant ou différent de la somme signée) et ne laisse rien en route ; le
refaire est sans effet. Il ne demande ni `serve` arrêté ni jeton, et
s'exécute sous le compte du service pour que les fichiers lui appartiennent.

Puis, pour chaque machine : Machines → la machine → Agent → **Mettre à jour
l'agent**. Le bouton n'apparaît que si l'agent est en ligne, a le droit
`agent.update`, et si sa version n'est pas celle du serveur ; sinon la fiche
en donne la raison (version plus récente, rien d'importé pour cette
architecture, droit manquant…). L'agent télécharge le binaire, **revérifie
lui-même la signature avec sa propre clé**, l'éprouve (`opencloud version`),
bascule le lien `bin/current`, puis s'arrête avec le code 3 : systemd le
relance. L'ordre devient « fait » quand l'agent revient en annonçant la
nouvelle version. Il garde la version précédente à côté.

**Un agent installé avant le 22 octobre 2026** a son état dans
`/var/lib/opencloud/agent`, et le plus ancien lance encore
`/usr/local/bin/opencloud` : sans `bin/current` l'agent refuse l'ordre
(`not_self_updatable`). Une fois par machine, [le déplacement de son
état](#déplacer-létat-de-lagent-installé-avant-le-22-octobre-2026) règle les
deux d'un coup.

**Revenir en arrière** sur une machine : l'interface n'envoie pas de version
plus ancienne (l'agent la refuserait aussi). À la main, sur la machine :

```sh
ln -sfn opencloud-v0.0.1 /var/lib/opencloud-agent/bin/current   # la précédente
systemctl restart opencloud-agent
```

**Le niveau label demande des agents à jour ; mettez à jour le serveur d'abord, puis les agents.**
Un agent plus ancien ne transmet pas les labels : un conteneur étiqueté reste rattaché par son projet Compose ou
par son nom (ou non déclaré) tant que son agent n'est pas mis à jour ; un
agent à jour contre un serveur plus ancien fonctionne, le serveur ignore ce
qu'il ne connaît pas. À la mise à jour de l'agent, un conteneur étiqueté passe
au niveau « label » au rapport suivant ; un label faux ou mal formé le rend
« label orphelin », sans repli.

**L'ordre : le serveur d'abord, puis les agents.** Ce que le code garantit :

| Situation | Effet |
| --- | --- |
| Agent plus ancien que le serveur, même protocole | Accepté. Le serveur ignore les sections du signal que l'agent n'envoie pas, et lui répond dans l'ancien format de refus |
| Agent d'un autre protocole | Refusé, `426 protocol_unsupported` : il reste hors ligne. Un agent du protocole 2 s'arrête (code 2) et dit d'installer le même binaire que sur la machine openCloud ; un agent du protocole 1 réessaie sans fin |
| Commande du serveur inconnue de l'agent | Ignorée, une ligne `unknown command` au journal de l'agent |
| Champ JSON inconnu dans ce que l'agent envoie | Ignoré par le serveur |
| Serveur redémarré | Les agents se reconnectent seuls ; l'interface et les flux aussi |
| Identité | Gardée : une mise à jour ne demande jamais de ré-enrôler, sauf une fois, pour les ordres (ci-dessous) |

**Les ordres signés demandent un ré-enrôlement, une fois.** Un agent enrôlé
avant eux n'a pas épinglé la clé d'openCloud : il observe comme avant, mais
refuse chaque ordre (`server_key_missing`), et la carte « Ordres » de sa
fiche le dit. Ré-enrôlez-le (fiche › **Ré-enrôler**, puis la ligne
d'installation sur la machine, `identity.json` supprimé) pour qu'il reçoive
la clé.

**Le protocole 2 (#146) se passe d'un coup.** Chaque requête de l'agent
annonce sa version (`OpenCloud-Agent-Protocol: 2`) et le serveur refuse
toute autre : il n'y a **pas de négociation**. La preuve de l'agent a changé
(plus de défi à demander, `/agent/challenge` n'existe plus) : un agent
d'avant ne se connecte plus à un serveur à jour, et inversement. Mettez à
jour le serveur, puis le binaire de chaque agent, sans rien ré-enrôler :
l'identité se garde. Entre les deux, la machine est hors ligne. Au sein
d'un même protocole, rien n'empêche un agent plus récent que le serveur,
mais ce cas n'est pas garanti. La colonne Agent de la
page Machines affiche la version de chaque agent ; tant qu'elle reste
`v0.0.1`, elle ne distingue pas deux builds. `opencloud version` sur la
machine donne le commit.

**mcp-maison** se met à jour à part : remplacez la ligne `image:` de son
`compose.yml` par celle du `compose.mcp-maison.yml` de la nouvelle version
d'openCloud, puis `docker compose pull && docker compose up -d`.

### Déplacer l'état de l'agent (installé avant le 22 octobre 2026)

Avant cette date, l'état de l'agent était dans `/var/lib/opencloud/agent`. Sur
la machine openCloud, ce dossier est sous celui du compte `opencloud`, qui
pouvait donc le renommer ou remplacer `identity.json` (les droits de l'agent,
la clé du serveur qu'il a épinglée). L'état vit maintenant dans
`/var/lib/opencloud-agent`, à root en `0700`. **Une fois par machine, en
root.** L'identité, la clé, les droits et le rang des ordres sont gardés : pas
de ré-enrôlement. Sur la machine openCloud, le nouveau binaire voit tant que
l'état n'est pas déplacé un dossier au-dessus qui appartient à un autre
compte : il ne garde que `observe` (journal : `state dir is not safe`). C'est
déjà le seul droit qu'a par défaut cet agent ; un agent qui en avait d'autres
les retrouve au redémarrage suivant le déplacement. Sur les autres machines,
l'ancien dossier était à root : rien ne se coupe, mais le déplacement reste à
faire.

Si le compte `opencloud` du serveur a pu être compromis avant le déplacement,
ne gardez pas l'ancien état : ré-enrôlez la machine (fiche › **Ré-enrôler**).

```sh
systemctl cat opencloud-agent | grep '^ExecStart='   # notez les options de la ligne
systemctl stop opencloud-agent
test -d /var/lib/opencloud/agent && test ! -L /var/lib/opencloud/agent && test ! -e /var/lib/opencloud-agent \
  && mv /var/lib/opencloud/agent /var/lib/opencloud-agent \
  && chown root:root /var/lib/opencloud-agent && chmod 0700 /var/lib/opencloud-agent
find /var/lib/opencloud-agent ! -user root     # ne doit rien afficher
```

Les `test` arrêtent la ligne si l'ancien état est un lien (`chown` et `chmod`
suivraient sa cible, qu'un autre compte aurait choisie) ou si le nouveau
existe déjà (`mv` rangerait l'ancien dedans). `find` ne doit rien afficher :
un fichier qui n'est pas à root veut dire que l'état a été remplacé. Dans les
deux cas, ne gardez pas l'ancien état : ré-enrôlez la machine. L'agent le
refuserait de toute façon (`identity.json` d'un autre compte : `state dir is
not safe`).

Puis le binaire de l'agent, `v0.0.1` étant ce que `opencloud version` affiche.
Les liens de `bin/` sont relatifs : ils ont suivi le déplacement.

- **Une autre machine, dont l'agent lançait déjà `…/bin/current`** : le lien
  `/usr/local/bin/opencloud`, qui visait l'ancien chemin, doit viser le
  nouveau.

  ```sh
  ln -sfn /var/lib/opencloud-agent/bin/current /usr/local/bin/opencloud
  ```

- **Une autre machine, dont l'agent lançait `/usr/local/bin/opencloud`** (pas
  de `bin/`) :

  ```sh
  install -D -m 0755 /usr/local/bin/opencloud /var/lib/opencloud-agent/bin/opencloud-v0.0.1
  ln -sfn opencloud-v0.0.1 /var/lib/opencloud-agent/bin/current
  ln -sfn /var/lib/opencloud-agent/bin/current /usr/local/bin/opencloud
  ```

- **La machine openCloud** : sa copie du binaire dans `bin/`, **sans** le lien
  `/usr/local/bin/opencloud` (c'est le binaire du serveur), et plus de drop-in.

  ```sh
  install -D -m 0755 /usr/local/bin/opencloud /var/lib/opencloud-agent/bin/opencloud-v0.0.1
  ln -sfn opencloud-v0.0.1 /var/lib/opencloud-agent/bin/current
  rm /etc/systemd/system/opencloud-agent.service.d/machine-opencloud.conf
  ```

Enfin la nouvelle unité (`-state` et `ExecStart` y sont déjà ceux du nouveau
chemin), puis le redémarrage :

```sh
install -m 0644 docs/installation/opencloud-agent.service /etc/systemd/system/opencloud-agent.service
# Les options notées au début (-allow-plain, -ca-file, -docker-socket), en fin de ligne, par exemple :
sed -i 's|^ExecStart=.*|& -ca-file /etc/ssl/ca-interne.pem|' /etc/systemd/system/opencloud-agent.service
systemctl cat opencloud-agent | grep '^ExecStart='   # le nouveau chemin, et les mêmes options qu'avant
systemctl daemon-reload && systemctl start opencloud-agent
journalctl -u opencloud-agent -n 20
```

Le journal doit dire `identity loaded` et `connected`, et **pas** `state dir
is not safe`. Sans option sur l'ancienne ligne, sautez le `sed`. Un autre
drop-in qui redéfinit `ExecStart=` (le `grep` en montre alors plusieurs) se
corrige de la même façon : son chemin devient
`/var/lib/opencloud-agent/bin/current … -state /var/lib/opencloud-agent`. Les drop-ins `droits.conf`
(`ReadWritePaths=` d'un droit qui écrit ailleurs) restent valables. Ailleurs
que sur la machine openCloud, `/var/lib/opencloud` est maintenant vide :
`rmdir /var/lib/opencloud` (il refuse s'il reste quelque chose).

## Sauvegarder et restaurer

**La sauvegarde intégrée.** Administration → **Sauvegardes** envoie chaque
nuit (après 3 h, heure du serveur) un instantané chiffré de la base, avec
`secret.key` et `server.key`, à l'agent d'une **autre machine**. openCloud n'en
garde aucune copie. Trois gestes, dans l'ordre :

1. **Créer la clé** : une phrase de passe d'au moins 16 caractères, saisie
   deux fois. openCloud ne la garde pas ; sans elle, aucune sauvegarde ne se
   relit. Gardez-la dans un coffre, **pas à côté des sauvegardes**.
2. **Ajouter une destination** : une machine dont l'agent a été enrôlé avec le
   droit « Recevoir des sauvegardes », dans un dossier, celui du droit ou un
   sous-dossier (voir [Ajouter une machine](#ajouter-une-machine) ; le droit se
   change par un ré-enrôlement). L'unité de l'agent doit autoriser ce dossier
   (`ReadWritePaths=`, étape 4 de [Ajouter une machine](#ajouter-une-machine)) ;
   sans cela l'ordre échoue avec `path_unwritable`.
3. **Régler le plan** : la destination et le nombre de sauvegardes à garder
   (14 par défaut). « Sauvegarder maintenant » en lance une tout de suite.

Sur la destination, chaque sauvegarde est un fichier
`opencloud-AAAAMMJJ-HHMMSS.tar.age`, à côté de
`opencloud-key-<empreinte>.age`, la clé privée **scellée par la phrase**.
L'agent vérifie ce qu'il a écrit (taille, somme, en-tête `age`) et la page dit
l'état du dernier ordre ; il ne déchiffre rien, il n'a pas la clé. Les clés
déposées ne sont jamais effacées par l'élagage. Remplacer la clé ne rend pas
les anciennes sauvegardes illisibles : chacune a sa clé à côté d'elle, mais il
faut garder l'ancienne phrase.

Les méthodes ci-dessous (`sqlite3 .backup`, `tar`) restent comme secours, et
pour ce que la sauvegarde intégrée ne couvre pas (la configuration, l'identité
des agents). Les services déployés (`data/`, `.env`) viendront avec le
déploiement.

### Quoi

| Où | Quoi | Perte |
| --- | --- | --- |
| Serveur, `state_dir` | `opencloud.db` (avec `-wal` et `-shm` tant qu'il tourne) | Tout : machines, historique, sondes, tâches, alertes, page de statut, réglages de l'interface (langue, seuils du disque, relais d'e-mails mot de passe compris, fin de support), jetons d'API et authentification, et le journal d'audit (Administration → Journal : qui a fait quoi, un an), que les canaux « Recevoir le journal » (Alertes › Canaux) reçoivent aussi : cochez-en un, une base effacée n'efface plus la trace. `opencloud auth disable` et `opencloud secrets forget` s'y inscrivent sous le compte qui les lance |
| Serveur, `state_dir` | `secret.key` | La clé des secrets de la base : sans elle, la base garde ses données mais perd le mot de passe du relais, l'URL et le secret des canaux, les URL de ping (voir [Clé perdue](#clé-perdue)) |
| Serveur, `state_dir` | `server.key` | La clé qui signe les ordres aux agents, tirée au premier démarrage : chaque agent a épinglé sa moitié publique. Sans elle, `serve` en tire une neuve et **chaque agent refuse les ordres** (`bad_signature`) jusqu'à son ré-enrôlement ; l'observation continue. Qui l'a commande toutes les machines : même soin que `secret.key` |
| Serveur | `/etc/opencloud/config.toml` | La configuration |
| Chaque machine | `/var/lib/opencloud-agent/identity.json` | Sa clé : il faudra ré-enrôler |
| La machine openCloud, si mcp-maison y est | `/srv/workspace/mcp-maison/` : `.env`, `data/mcp/` | Le `.env` (jeton d'API, secret de l'application chez l'IdP), les droits, la base OAuth et le journal de mcp-maison : on les refait, mais chaque personne se reconnecte |

**La base chiffre ses secrets** : les jetons de ping, l'URL et le secret des
canaux d'alerte (un webhook Discord, Slack ou ntfy porte son jeton dans
l'URL) et le mot de passe du relais d'e-mails sont chiffrés (AES-256-GCM) avec
la clé de `secret.key`. Les jetons d'enrôlement, les jetons d'API et le mot de
passe de l'interface y sont hachés. Une copie de `opencloud.db` seule ne livre
donc plus ces secrets.

**La clé est à côté de la base, pas dedans.** Elle protège une copie de la
base prise seule, pas quelqu'un qui lit tout `state_dir` : gardez-la `0600`,
au compte du service (le serveur refuse une clé que d'autres comptes peuvent
lire). Ce qui reste en clair : la cible d'une sonde (des identifiants mis
dans l'adresse), les extraits de journaux d'un arrêt anormal, le corps des
pings.

### Sauvegarder

**La clé, une fois, à part.** `secret.key` ne change pas tant qu'on ne la
remplace pas ni ne la fait tourner (après `secrets rotate`, sauvegardez la
neuve) : copiez-la une fois dans un gestionnaire de mots de passe ou un
coffre, **pas à côté des sauvegardes de la base** (qui perdraient leur
intérêt). La sauvegarde à chaud ci-dessous ne la contient pas ; la sauvegarde
à froid (`tar` du dossier) l'inclut, protégez-la comme un secret.

```sh
cat /var/lib/opencloud/secret.key /var/lib/opencloud/server.key
```

`server.key` se garde de même, une fois, à part : elle ne change pas non
plus, sauf rotation (sauvegardez-la après `finish-server-key`).

La base est en mode WAL : copier `opencloud.db` seul pendant que `serve`
tourne donne une copie incomplète. Deux façons sûres.

**À chaud**, avec le client `sqlite3` (`apt install sqlite3`), sous le
compte du service :

```sh
runuser -u opencloud -- sqlite3 /var/lib/opencloud/opencloud.db \
  ".backup '/var/lib/opencloud/opencloud-$(date +%F).db'"
```

Vérifié avec le client `sqlite3` 3.45, service en marche : copie
cohérente, `PRAGMA integrity_check` à `ok`, puis restaurée comme ci-dessous.

**À froid** :

```sh
systemctl stop opencloud
tar -C /var/lib -czf /root/opencloud-$(date +%F).tar.gz opencloud
systemctl start opencloud
```

### Exporter l'annuaire

`opencloud export` écrit l'annuaire déclaré (projets, services, sondes,
tâches, clés ignorées) en YAML, à relire, versionner dans Git ou comparer :

```sh
opencloud export -config /etc/opencloud/config.toml > opencloud.yaml
```

La commande n'ouvre pas la base : lancée en root ou sous le compte du service
(`runuser -u opencloud -- opencloud export …`), elle ne crée donc pas de
fichier `-wal` au nom de root. Le fichier écrit se place où votre compte peut
écrire : avec `-o` sous le compte du service, le dossier doit lui appartenir
(il échoue sinon) ; la redirection `>` écrit avec vos droits. `-o` écrit en
0600.

La commande **demande le fichier au serveur qui tourne**, par la socket
`state_dir/control.sock` (0600, créée au démarrage de `serve`, retirée à son
arrêt) : serveur arrêté, elle répond que le serveur ne tourne pas et sort en
`1`. Sans `-o`, le fichier va sur la sortie standard. Un second `serve` sur le
même `state_dir` n'a pas de socket (le premier garde la sienne), et `serve`
n'ouvre la socket que si `state_dir` est fermé au groupe et aux autres. Le
bouton « Exporter » de l'onglet Annuaire donne le même fichier.

**Ce n'est pas une sauvegarde.** Le fichier ne contient ni historique, ni
mesures, ni canaux, ni pages de statut, ni réglages, et aucun secret : un jeton
de tâche s'y lit `token: {secret: kept}`, pas sa valeur. Il ne remplace ni
`opencloud.db` ni `secret.key`. La socket n'est pas à sauvegarder : `serve` la
recrée. L'unité systemd ne change pas.

**Avec propositions.** `opencloud export -propose` ajoute à la fin du fichier
ce que les conteneurs non déclarés laissent deviner : un service par projet
Compose ou par conteneur, sans `id`, précédé d'un commentaire « Proposé depuis
des conteneurs non déclarés : relisez avant apply », et les unités qui manquent
à un projet déjà déclaré. Rien n'est écrit dans l'annuaire. Relisez, corrigez
ce qui doit l'être, puis passez par `apply` comme pour tout fichier ; sans
`-propose`, le fichier est celui d'avant.

```sh
opencloud export -config /etc/opencloud/config.toml -propose > propose.yaml
opencloud apply -config /etc/opencloud/config.toml -dry-run propose.yaml
```

Un conteneur dont le label vise une unité qui n'existe pas, ou dont le label
est mal formé (« label orphelin ») n'est pas proposé : on corrige le label.

### Étiqueter un conteneur

Un conteneur se rattache à une unité déclarée par deux labels, sans projet
Compose ni nom à garder. Le préfixe est celui du produit (`opencloud.` ici) ;
l'identifiant du service se lit dans l'export (`svc_…`, ligne `id`),
et l'unité porte le nom que le service lui donne :

```yaml
services:
  app:
    image: nginx:1.27
    labels:
      opencloud.service: svc_0123456789abcdef
      opencloud.unit: app
```

Le label ne crée rien : le service et l'unité doivent déjà être déclarés **sur
la machine où tourne le conteneur**, et l'unité doit être un conteneur (pas un
binaire). Sinon le conteneur reste non déclaré, avec le motif (« label
orphelin ») sur sa fiche et dans la liste des non déclarés, et ne se rattache
ni par son projet Compose ni par son nom. Un label mal écrit en fait autant :
une clé en majuscules (`opencloud.Service`), une valeur de plus de 255 octets ou
qui contient un caractère de contrôle, un identifiant qui n'est pas de la forme
`svc_` suivi de 16 hexadécimaux, un nom d'unité illisible. Le motif dit « mal
formé : clé en minuscules, valeur de 255 octets au plus » ; corrigez le label. L'agent ne transmet au serveur que
les labels qui commencent par le préfixe ; ceux de Traefik et de Compose
restent sur la machine.

### Appliquer un fichier

`opencloud apply` rejoue un fichier comme celui de `export` : ce que vous
y avez changé (une version, le nom d'une sonde, un service ajouté) est écrit
dans l'annuaire, **par les mêmes gestes que l'interface**, pour que les
agents reçoivent leurs sondes et que le rattachement des conteneurs se refasse.

```sh
opencloud apply -config /etc/opencloud/config.toml opencloud.yaml
```

La commande montre d'abord le plan, puis demande confirmation :

```
Plan pour opencloud.yaml (sha256:1a2b3c4d…)
  + projet docs
  ~ service app-1/wiki (svc_7f3k…)
      units[app].version : 2.4.0 → 2.4.1
  ~ sonde wiki-http : interval 1m → 30s
  - sonde old-check (a1b2…) — emporte 1204 essais, 80 jours
  = 14 inchangés · 3 hors fichier, gardés (-prune pour les supprimer)
Appliquer ces changements ? [o/N]
```

| Option | Effet |
| --- | --- |
| `-dry-run` | Montre le plan et s'arrête : rien n'est écrit |
| `-yes` | Confirme sans poser la question. **Obligatoire sans terminal** (un script, un cron) : sans lui et sans terminal, la commande refuse |
| `-prune` | Supprime aussi ce que le fichier ne nomme pas. Sans lui, ces objets sont gardés et comptés « hors fichier ». Ne supprime jamais une machine ; refuse de supprimer un service, une sonde ou une tâche qu'un composant de la page de statut montre (détachez-le d'abord) |

- **Le serveur doit tourner**, comme pour `export` : sinon la commande le dit
  et sort en `1`.
- **L'`id` rattache l'entrée à son objet.** Renommer une sonde en gardant son
  `id` la renomme, avec son historique ; retirer l'`id` d'une entrée la rattache
  par son nom. Un `id` qui n'existe pas est refusé, pour qu'un copier-coller ne
  crée pas un doublon sans que vous le sachiez. Une entrée sans `id` ni objet de
  ce nom est une création.
- **Un fichier refusé n'écrit rien**, et la commande liste tous les refus avec
  leur ligne (sortie `2`). `token: {secret: kept}` garde le jeton du serveur ;
  une tâche créée reçoit un jeton que le serveur tire, à lire dans l'interface.
- **Pas une transaction.** Si un changement est refusé au milieu, les
  précédents sont écrits et la commande dit lequel s'est arrêté et pourquoi ;
  corrigez puis **relancez la même commande** : ce qui est écrit ressort
  « inchangé ». Si l'annuaire est modifié dans l'interface pendant que vous
  lisez le plan, la commande montre le nouveau plan et redemande, une fois. Avec
  `-yes`, personne n'a lu ce nouveau plan : il est montré, rien n'est écrit, et
  la commande sort en `1` : relancez-la.
- **Le journal.** Chaque fichier appliqué laisse une ligne (qui, quand, quelle
  empreinte, le plan, comment ça s'est fini), lisible sous Services, Annuaire,
  « Journal des fichiers ». Elle ne contient ni le fichier ni aucun secret, et
  elle est dans `opencloud.db` : la sauvegarde de la base la garde.
- **Codes de sortie** : `0` appliqué ou rien à changer, `2` fichier refusé,
  plan non confirmé ou option inconnue, `1` erreur (serveur absent, application
  arrêtée, annuaire changé sous `-yes`).

### Restaurer

```sh
systemctl stop opencloud
rm -f /var/lib/opencloud/opencloud.db-wal /var/lib/opencloud/opencloud.db-shm
install -m 0600 -o opencloud -g opencloud opencloud-2026-10-10.db /var/lib/opencloud/opencloud.db
systemctl start opencloud
```

Posez aussi la clé avec la base, en `0600` pour `opencloud` : sans elle, le
serveur refuse de démarrer (la base contient des secrets chiffrés).

```sh
install -m 0600 -o opencloud -g opencloud secret.key /var/lib/opencloud/secret.key
```

La clé doit être celle de la base restaurée. Une sauvegarde à froid (`tar`)
rend les deux ensemble. Posez `server.key` de même : avec la clé d'avant, les
agents reprennent les ordres sans rien faire ; sans elle, ré-enrôlez-les.

```sh
install -m 0600 -o opencloud -g opencloud server.key /var/lib/opencloud/server.key
```

Retirez les `-wal` et `-shm` de l'ancienne base avant de poser la copie :
SQLite les rejouerait sur elle. Une base plus ancienne que le binaire est
migrée au démarrage.

Les agents gardent leur identité : ceux que la copie connaît se
reconnectent seuls. Une machine enrôlée après la sauvegarde est refusée
(« openCloud ne reconnaît plus cette machine ») : ré-enrôlez-la.

### Restaurer une sauvegarde chiffrée

Hors ligne, `serve` arrêté, sur n'importe quelle machine qui a le binaire et
les deux fichiers de la destination : l'archive
`opencloud-AAAAMMJJ-HHMMSS.tar.age` et la clé `opencloud-key-<empreinte>.age`
de la **même** sauvegarde.

```sh
systemctl stop opencloud
opencloud backup restore -key opencloud-key-0123456789abcdef.age \
  -state /var/lib/opencloud opencloud-20261021-030000.tar.age
```

La phrase de passe se lit sur l'entrée standard (une ligne, jamais en
argument ; elle s'affiche à la frappe si l'entrée est un terminal). Le dossier
`-state` doit être **vide ou absent** (créé en `0700`) : la commande ne
restaure jamais par-dessus une base. Elle écrit `opencloud.db`, `secret.key` et
`server.key` en `0600`, rien d'autre, et rien n'apparaît sous son nom définitif
tant que l'archive n'est pas lue jusqu'au bout. Puis :

```sh
chown -R opencloud:opencloud /var/lib/opencloud
systemctl start opencloud
```

Les agents se reconnectent seuls : `server.key` est celle d'avant, ils n'ont
rien à ré-enrôler.

**Sans openCloud**, avec l'outil [`age`](https://age-encryption.org) seul :

```sh
age -d -o cle.txt opencloud-key-0123456789abcdef.age          # demande la phrase
age -d -i cle.txt opencloud-20261021-030000.tar.age | tar -x   # opencloud.db, secret.key, server.key
install -m 0600 -o opencloud -g opencloud opencloud.db secret.key server.key /var/lib/opencloud/
shred -u cle.txt
```

`cle.txt` est la clé privée en clair : ne la laissez pas traîner.

### Clé perdue

Sans `secret.key`, le serveur refuse de démarrer : « `opencloud.db` contient
des secrets chiffrés mais `secret.key` est absent de `state_dir` ». Si la clé
est introuvable (aucune copie, aucune sauvegarde à froid), il n'y a pas de
moyen de relire ces secrets. La sortie de secours, serveur arrêté :

```sh
runuser -u opencloud -- opencloud secrets forget -config /etc/opencloud/config.toml
systemctl start opencloud
```

Elle écrit une clé neuve et **efface ce que l'ancienne chiffrait** :

| Ce qui est perdu | Ce qu'il faut faire |
| --- | --- |
| Le mot de passe du relais d'e-mails | Le ressaisir sous Paramètres → Envoi d'e-mails |
| L'URL et le secret des canaux d'alerte (webhook, Matrix) | Les canaux sont **désactivés** et vidés : ressaisir l'URL et le secret de chacun, puis le réactiver. Un canal e-mail reste actif mais n'envoie rien tant que le mot de passe du relais n'est pas ressaisi |
| Le jeton de chaque tâche | Chaque tâche reçoit un **nouveau jeton** : l'ancienne URL de ping ne répond plus, remplacez-la dans les crons |

Tout le reste de la base reste. La commande affiche le nombre de canaux
désactivés et de jetons renouvelés. Elle **garde l'ancienne clé** sous
`secret.key.forgotten-<horodatage>` : elle ne rouvre plus cette base, mais
elle relit une sauvegarde plus ancienne de la base ; effacez-la sinon. Elle
**refuse** si `secret.key` est présente, intacte et ouvre déjà tous les
secrets (rien à oublier). Si elle échoue en route, relancez-la.

### Faire tourner les clés

**La clé de chiffrement, `secret.key`.** Serveur arrêté (la commande refuse
s'il répond sur sa socket de contrôle ; lancé sans elle, elle ne le voit
pas) :

```sh
systemctl stop opencloud
runuser -u opencloud -- opencloud secrets rotate -config /etc/opencloud/config.toml
systemctl start opencloud
```

Elle tire une clé neuve, la pose devant l'ancienne dans `secret.key`,
rescelle chaque secret de la base en une transaction, puis ne garde que la
neuve. L'ancienne est mise de côté sous `secret.key.rotated-<horodatage>` :
elle ne rouvre plus cette base, mais elle relit une sauvegarde d'avant ;
effacez-la quand ces sauvegardes ne servent plus. Interrompue, la commande
se relance : un fichier à deux clés reprend où il s'était arrêté. Rien n'est
perdu, aucune URL de ping ne change. Sauvegardez la clé neuve.

**La clé qui signe les ordres, `server.key`.** En deux temps, sans arrêter
les agents :

```sh
runuser -u opencloud -- opencloud secrets rotate-server-key -config /etc/opencloud/config.toml
systemctl restart opencloud
# Attendre que chaque agent se soit reconnecté, puis :
runuser -u opencloud -- opencloud secrets finish-server-key -config /etc/opencloud/config.toml
systemctl restart opencloud
```

La première tire une clé neuve et garde la précédente dans `server.key`.
Après le redémarrage, les deux sont valables : à l'ouverture de son flux,
chaque agent resté sur la précédente reçoit la neuve par l'ordre
`agent.server_key`, signé par la précédente, et l'épingle ; la fiche de la
machine (carte « Ordres ») le montre, le journal d'audit le garde. La
seconde retire la précédente ; elle **refuse** tant qu'une machine dont
l'agent est actif l'a encore épinglée, et la nomme : attendez qu'elle se
reconnecte. `-force` passe outre, et ces machines sont à ré-enrôler. Une
seule rotation à la fois. Sauvegardez la clé neuve.

**Une clé du serveur volée** ne se remplace pas ainsi : la neuve est livrée
sous la signature de l'ancienne, donc qui tient l'ancienne pourrait livrer
la sienne. Arrêtez le serveur, supprimez `server.key` (il en tire une neuve
au démarrage), puis ré-enrôlez chaque machine.

**La clé d'un agent** (dans son `identity.json`) tourne en le ré-enrôlant :
Machines → la machine → **Ré-enrôler**. La machine garde son historique.

## Désinstaller

openCloud n'a rien modifié ailleurs : il n'écrit jamais dans Docker ni sur
une machine.

**Une machine** :

```sh
# D'abord, dans l'interface : Machines → la machine → Retirer
systemctl disable --now opencloud-agent
rm /etc/systemd/system/opencloud-agent.service && systemctl daemon-reload
rm /usr/local/bin/opencloud
rm -r /var/lib/opencloud-agent
```

**Le serveur** (après une dernière sauvegarde si besoin), et l'agent de sa
machine :

```sh
systemctl disable --now opencloud opencloud-agent
rm /etc/systemd/system/opencloud.service /etc/systemd/system/opencloud-agent.service && systemctl daemon-reload
rm /usr/local/bin/opencloud
rm -r /etc/opencloud /var/lib/opencloud /var/lib/opencloud-agent
userdel opencloud
# et le fichier openCloud de la configuration dynamique de Traefik
```

**mcp-maison**, s'il est installé : `cd /srv/workspace/mcp-maison && docker compose down`,
puis `rm -r /srv/workspace/mcp-maison`. Les jetons d'API s'en vont avec la base ;
l'application déclarée chez l'IdP se retire chez lui.

## Dépannage

Les messages sont ceux que le binaire écrit, tels quels.

| Symptôme | Cause | Faire |
| --- | --- | --- |
| L'interface s'affiche mais rien ne charge ; l'API répond `403 {"error":"bad_host"}` ; le journal dit `request refused: unknown Host, set public_url to the address used to reach openCloud` | Le nom par lequel vous atteignez openCloud n'est pas celui de `public_url` | Mettre ce nom dans `public_url`, redémarrer |
| Agent : « Adresse refusée : openCloud est en http:// et n'est pas sur cette machine, le jeton passerait en clair. Utilisez https://, ou -allow-plain si le réseau est déjà chiffré (WireGuard). » | `-server http://…` vers une autre machine | `https://`, ou `-allow-plain` sur un réseau chiffré, au premier lancement **et** dans l'unité |
| Agent, en boucle : `clock skew, reconnecting` avec « L'horloge de cette machine et celle d'openCloud diffèrent de plus de 5 minutes… » ; la machine reste hors ligne | Horloges décalées | `timedatectl set-ntp true` sur les deux machines ; l'agent réessaie seul |
| Agent : « Jeton refusé : il a déjà servi. Un jeton n'enrôle qu'une machine, créez-en un nouveau. » | Jeton déjà consommé | Un nouveau jeton (Ajouter une machine, ou Ré-enrôler) |
| Agent : « Jeton refusé : il a expiré. » / « openCloud ne le connaît pas. » | Plus d'1 h, ou jeton annulé, ou mauvais serveur | Un nouveau jeton ; vérifier `-server` |
| Agent : « Jeton d'enrôlement absent : envoyez-le sur l'entrée standard… » | Premier lancement avec `-server` mais sans jeton sur l'entrée (lancé par systemd, ou `printf` oublié) | Coller la commande d'installation telle qu'openCloud l'affiche |
| Agent : `state dir is not safe, writing rights are off` au démarrage, et la fiche montre des droits rapportés (`observe`) différents de ceux cochés | Le dossier d'état, ou un dossier au-dessus, appartient à un autre compte que root ou peut être modifié par d'autres (le journal nomme le dossier, son propriétaire et son mode). Par sécurité l'agent ne garde que `observe` ; ses droits restent dans `identity.json` | Corriger le dossier nommé (`chown root:root`, `chmod go-w`), ou, pour un agent installé avant le 22 octobre 2026, [déplacer l'état](#déplacer-létat-de-lagent-installé-avant-le-22-octobre-2026) ; puis `systemctl restart opencloud-agent` |
| Agent : « Cette machine n'est pas enrôlée. Lancez la commande d'installation donnée par openCloud. » | Service lancé avant l'enrôlement, ou `-state` différent | Faire le premier lancement avec le même `-state` |
| Agent : « openCloud ne reconnaît plus cette machine : elle a été retirée ou ré-enrôlée ailleurs. » | Machine retirée, ou restauration d'une base plus ancienne | Ré-enrôler |
| Agent : « Certificat refusé : ce n'est pas celui dont l'empreinte a été épinglée… » | Le certificat d'openCloud a changé, ou quelqu'un s'interpose | Vérifier l'empreinte, puis ré-enrôler avec la nouvelle |
| Agent : « openCloud ne parle pas la version du protocole de cet agent… » ; la machine reste hors ligne | Agent et serveur de deux protocoles différents (un seul des deux mis à jour) | Installer sur la machine le même binaire que sur la machine openCloud, puis `systemctl restart opencloud-agent` |
| `secrets rotate` : « un serveur tourne sur ce state_dir » | La rotation de `secret.key` demande le serveur arrêté | `systemctl stop opencloud`, relancer la commande, redémarrer |
| `secrets finish-server-key` : « ces machines ont encore la clé précédente » | Leur agent ne s'est pas reconnecté depuis la rotation | Attendre leur connexion (redémarrer l'agent au besoin), ou `-force` et les ré-enrôler |
| Agent : `tls: failed to verify certificate` | Certificat auto-signé sans `-pin` | `-pin`, ou un certificat reconnu |
| « L'agent n'a pas le droit de lire la socket Docker de cette machine. » | L'agent ne tourne pas en root | L'unité d'exemple le lance en root |
| « Docker n'est pas présent sur cette machine. » alors qu'il tourne | `-docker-socket` ne vise pas la bonne socket | Corriger le chemin dans l'unité de l'agent |
| La machine openCloud reste hors ligne, sans mesures | Son agent n'est pas installé (installation d'avant les agents, ou ligne de la première configuration pas jouée) | Machines → la machine openCloud → Ré-enrôler, et jouer la ligne sur elle |
| Agent : « Enrôlement refusé : cette machine est la machine openCloud, et elle a déjà un agent actif. » | Une ligne d'Ajouter une machine jouée sur la machine openCloud, qui a déjà son agent | Ré-enrôler la machine openCloud depuis sa fiche ; le jeton sert à une autre machine |
| `serve: listen tcp 172.17.0.1:8080: bind: cannot assign requested address` | `docker0` pas encore là au démarrage | L'unité redémarre seule ; vérifier `After=docker.service` |
| `serve: listen tcp …: bind: address already in use` | Un autre processus tient le port | `ss -ltnp` ; changer `listen` |
| Le serveur refuse de démarrer : « opencloud.db contient des secrets chiffrés mais secret.key est absent de state_dir… » | La clé n'a pas été restaurée avec la base | La poser (voir Restaurer) ; perdue : voir [Clé perdue](#clé-perdue) |
| « secret.key est lisible par d'autres comptes » | Le fichier a un mode plus large que `0600` | `chmod 600 /var/lib/opencloud/secret.key`, propriétaire le compte du service |
| Paramètres → Agent IA : « Sans ses outils openCloud » ; `groupe "opencloud" non chargé` au journal de mcp-maison | `OPENCLOUD_URL` ou `OPENCLOUD_TOKEN` du `.env` de mcp-maison manque ou est refusé | Recopier les lignes de Paramètres → Jetons d'API (un jeton neuf au besoin), puis `docker compose up -d` |
| mcp-maison : `401 api_token.invalid` | Le jeton est révoqué, échu, ou recréé à la création du compte | Un jeton neuf sous Paramètres → Jetons d'API, dans le `.env` |
| mcp-maison : `403 bad_host` | `OPENCLOUD_URL` vise un nom que `public_url` ne reconnaît pas | Mettre l'adresse IP de `listen` dans `OPENCLOUD_URL` |
| mcp-maison : `groupe "opencloud" non chargé : OPENCLOUD_URL: https:// attendu (http:// : boucle locale ou IP privée seulement)` | `OPENCLOUD_URL` en `http://` sur un nom ou une IP publique | `OPENCLOUD_URL=http://<l'IP de listen>:<port>` (boucle locale ou IP privée), ou `https://` |
| Mode `forward` : toute l'interface répond `401 {"error":"api_token.invalid"}` | Le mandataire transmet un `Authorization: Bearer` à openCloud (oauth2-proxy avec `--pass-authorization-header` ou `--set-authorization-header`) : un Bearer est jugé comme un jeton d'API, avant l'en-tête d'identité | Ne pas transmettre `Authorization` à openCloud ; seul l'en-tête d'identité compte |
| Le navigateur redemande le mot de passe à chaque fois, ou `429` | Mauvais identifiants, puis limite des échecs ; ou file de vérification pleine (des tentatives anonymes en cours) | Attendre `Retry-After` et réessayer ; mot de passe perdu : `opencloud auth disable` |
| `sudo: command not found` | Debian minimal, installé sans `sudo` | En root, retirer `sudo` ; sous le compte du service, `runuser -u opencloud -- …` |
| Tous les utilisateurs bloqués ensemble en `429` | `trusted_proxies` absent : tout le monde a l'adresse de Traefik | Déclarer Traefik dans `trusted_proxies` |
