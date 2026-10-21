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
9. [Brancher un agent IA (MCP)](#brancher-un-agent-ia-mcp)
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
| [`opencloud-agent.service`](installation/opencloud-agent.service) | `/etc/systemd/system/` de chaque machine, l'agent |
| [`traefik-opencloud.yml`](installation/traefik-opencloud.yml) | Le dossier de configuration dynamique de Traefik |
| [`compose.traefik.yml`](installation/compose.traefik.yml) | Extrait Compose d'un Traefik en conteneur qui joint openCloud sur l'hôte |

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

Les binaires Linux amd64 et arm64 sont dans la
[release v0.0.1](https://github.com/ldesfontaine/opencloud/releases/tag/v0.0.1),
avec leurs sommes SHA-256 :

```sh
base=https://github.com/ldesfontaine/opencloud/releases/download/v0.0.1
curl -fLO "$base/opencloud-linux-amd64" -fLO "$base/SHA256SUMS"
sha256sum -c --ignore-missing SHA256SUMS
mv opencloud-linux-amd64 opencloud && chmod +x opencloud
./opencloud version        # opencloud v0.0.1 (<commit>)
```

La release se republie à la main (workflow `release`, onglet Actions) :
elle garde le nom `v0.0.1`, le commit change. `opencloud version` le donne.

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
| `opencloud mcp -config <chemin>` | MCP sur stdin/stdout pour un client IA local, en lecture seule ; lit `secret.key`, ne la crée jamais |
| `opencloud export [-config <chemin>] [-o <fichier>] [-propose]` | Écrit l'annuaire en YAML (sortie standard, ou le fichier en 0600), avec `-propose` y ajoute ce que les conteneurs non déclarés laissent deviner, voir [Exporter l'annuaire](#exporter-lannuaire) |
| `opencloud apply [-config <chemin>] [-prune] [-yes] [-dry-run] <fichier>` | Applique un fichier YAML à l'annuaire, après avoir montré le plan et demandé confirmation, voir [Appliquer un fichier](#appliquer-un-fichier) |
| `opencloud version` | La version et le commit |

## Installer le serveur

Sur le VPS, en root. Un Debian minimal n'a pas `sudo` : les commandes
« sous le compte du service » passent par `runuser`, toujours présent.

```sh
# Le binaire
install -m 0755 opencloud /usr/local/bin/opencloud

# Un compte système, sans shell
useradd --system --home-dir /var/lib/opencloud --shell /usr/sbin/nologin opencloud

# Docker présent : lire sa socket. Le groupe docker vaut root sur la machine,
# c'est le prix de la lecture ; openCloud n'appelle aucun verbe qui écrit.
usermod -aG docker opencloud

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
`serve` sous `opencloud`, dans le groupe `docker` (`SupplementaryGroups=`).
Sans Docker sur la machine, retirez cette ligne : l'unité échouerait sur un
groupe inconnu.

Au démarrage, le journal dit `listening` avec l'adresse, le répertoire
d'état et la version. Tant qu'aucun compte n'existe, une ligne le rappelle
(`interface awaiting first setup`) : `INFO` si `listen` est sur la boucle
locale, `WARN` sinon, car le premier qui atteint l'adresse crée le compte.
Créez-le tout de suite : [Authentification](#authentification).

**Root plutôt qu'un compte dédié ?** Possible : retirez `User=`, `Group=` et
`SupplementaryGroups=`. Le compte dédié limite ce que le processus touche
en dehors de Docker.

**Les commandes `auth`, `secrets` et `mcp` se lancent sous le compte du service** :

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
| `docker_socket` | `/var/run/docker.sock` | La socket Docker de cette machine. Absente : la machine n'a pas de service, ce n'est pas une erreur |
| `ca_file` | vide | Un paquet PEM d'autorité interne, ajouté au magasin du système pour les sondes de cette machine. Un chemin illisible arrête le démarrage |
| `log_level` | `info` | `debug`, `info`, `warn` ou `error`. En `debug`, une ligne par requête HTTP |

Une clé inconnue est signalée au démarrage (`unknown key ["…"] ignored`)
puis ignorée ; une valeur invalide arrête le démarrage avec le nom du champ.
Le journal est en JSON sur stderr, donc dans `journalctl`.

### `public_url`

Elle sert à deux choses.

1. **Écrire les adresses** : la commande d'installation d'une machine, l'URL
   de ping d'une tâche, l'adresse MCP. Vide, openCloud les déduit de la
   requête (et de `X-Forwarded-Host` / `-Proto` d'un mandataire de
   confiance). Si l'adresse paraît locale, la page du jeton le dit :
   « L'adresse d'openCloud paraît locale : une machine distante ne la
   joindra pas. Renseignez public_url dans la configuration. »
2. **Reconnaître le nom de l'interface.** Toute requête sur `/api/…` et
   `/oauth/authorize` doit arriver avec un `Host` connu : l'hôte de
   `public_url`, `localhost`, une adresse IP, ou l'hôte de `listen`.
   Derrière un mandataire de confiance, `X-Forwarded-Host` est jugé aussi.
   Sinon : `403 {"error":"bad_host"}`, et le journal dit
   `request refused: unknown Host, set public_url to the address used to reach openCloud`.
   C'est la parade au *DNS rebinding*. Conséquence : **atteindre
   l'interface par un nom de domaine impose de déclarer ce nom dans
   `public_url`**. La page s'affiche, mais l'API refuse.

Un seul nom est reconnu. La page de statut, les pings, les agents et `/mcp`
ne sont pas soumis à cette garde.

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
suffit : interface, API, agents, pings, statut et MCP passent par le même
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
`/statut/api/events` et `/mcp` sont des flux SSE : une réponse qui dure des
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
| `/statut`, `/statut/…` | Le public | Aucune, par conception |
| `/mcp` | Un agent IA | Un Bearer OAuth ou un jeton d'API |
| `/oauth/token` | Un agent IA | Le secret du client |
| `/.well-known/…` | Un agent IA | Aucune : des métadonnées |

La page de statut charge aussi `/assets/…`, `/theme.js` et `/favicon.svg` :
ils doivent rester joignables pour qu'un visiteur la voie. Ne cumulez pas
le Basic de Traefik et celui d'openCloud : un seul en-tête `Authorization`
porte les deux.

## Authentification

**Premier démarrage : ouvrez l'adresse, créez le compte.** Une installation
neuve n'a pas de compte, et ne sert rien d'autre que l'écran de première
configuration : l'interface, l'API, le direct, les journaux et
`/oauth/authorize` répondent `401` (`auth.setup_required`) tant que le compte
n'existe pas. La page de statut, les pings, les agents et MCP gardent leur
propre accès. Deux modes : identifiant et mot de passe (HTTP Basic,
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
| Effet | Immédiat, sans redémarrage. Créer le compte révoque tous les jetons MCP (jetons d'API et sessions OAuth) : il n'y en a qu'après un `auth disable`, recréez ceux qui servent |

Ce qui reste ouvert : la liste du [tableau ci-dessus](#une-authentification-basic-au-niveau-de-traefik).
Le reste — l'interface, `/api/…`, le direct, les journaux,
`/oauth/authorize` — demande les identifiants. Une fois activée, l'API se
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
jetons MCP.

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

## Brancher un agent IA (MCP)

**Paramètres → Agent IA (MCP) → Activer.** L'agent lit openCloud (machines,
services, tâches, sondes, alertes, page de statut, journaux de conteneurs) ;
il n'écrit rien pendant la refonte et n'agit jamais sur une machine. Trois
façons de le brancher, selon le client :

| Client | Comment | Ce qu'il lui faut |
| --- | --- | --- |
| Un client en ligne de commande ou un script (Claude Code distant, Codex, Cursor, Hermes, `mcp-remote`, l'inspecteur MCP) | Un **jeton d'API** en `Authorization: Bearer` | L'adresse `https://…/mcp` et un jeton créé sous **Jetons d'API** (montré une fois, échéance 30, 90 ou 365 jours, ou sans) |
| Un connecteur web (claude.ai et les applications qui l'exigent) | **OAuth**, avec votre consentement | L'adresse `https://…/mcp`, l'identifiant `opencloud` et le **secret du client** ; l'adresse de retour du connecteur déclarée sous **Adresses de retour** (pour Claude : `https://claude.ai/api/mcp/auth_callback`) |
| Un client sur la machine openCloud (Claude Code, Claude Desktop) | `opencloud mcp` sur stdin/stdout, sans réseau | La commande affichée sous **Client local**, lancée sous le compte du service |

Avec OAuth, le connecteur ouvre votre navigateur sur openCloud. Après vos
identifiants, une **page de consentement** montre le client, l'adresse de
retour exacte, ce que l'autorisation donne et sa durée (un accès d'une heure,
renouvelé seul jusqu'à trente jours sans usage) ; **Autoriser** renvoie le
connecteur avec son code, **Refuser** avec `access_denied`. N'autorisez que
la connexion que vous venez de lancer, et refusez si l'adresse de retour ne
vous dit rien. La demande vaut dix minutes ; un redémarrage d'openCloud
l'oublie, il suffit de relancer la connexion depuis le client. L'accès est
en lecture : un client qui demande l'écriture reçoit la lecture tant
qu'elle est désactivée.

Les sessions OAuth se lisent et se coupent sous **Sessions OAuth** (portée,
date d'autorisation, dernier usage) ; les jetons d'API se révoquent sous
**Jetons d'API**. Le consentement, le refus et chaque révocation s'inscrivent
au journal (**Administration → Journal**).

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

Ils s'écrivent dans `identity.json` et la fiche de la machine les montre ;
**aucun n'est encore appliqué** par l'agent, qui ne fait qu'observer.

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

Pas d'agent sur la machine openCloud elle-même : elle se supervise déjà.
Lancé là, l'agent est refusé (« cette machine est déjà supervisée par
openCloud lui-même ») et le jeton reste valable pour une autre machine.

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
ssh root@machine install -m 0755 /tmp/opencloud /usr/local/bin/opencloud
```

### 3. Le premier lancement : l'enrôlement

Sur la machine, en root, la commande affichée :

```sh
printf '%s\n' 'oc_…' | sudo opencloud agent -server https://opencloud.example.org
```

L'agent tire une clé Ed25519, s'enrôle, écrit
`/var/lib/opencloud/agent/identity.json` en `0600`, puis se connecte. Quand le
journal dit `"msg":"connected"`, arrêtez-le (Ctrl-C) : le service prend le
relais.

| Option | Retenue dans l'identité | Rôle |
| --- | --- | --- |
| `-server` | Oui | Adresse d'openCloud, premier lancement seulement |
| `-pin` | Oui | Empreinte SHA-256 du certificat d'openCloud, s'il est auto-signé |
| `-lang` | Oui | Langue des messages de l'agent, `fr` ou `en` |
| `-rights` | Oui | Droits cochés dans l'interface ; sans lui, `observe` seul. Un droit mal écrit est refusé avant l'enrôlement (code 2), le jeton reste valable |
| `-state` | Non | Répertoire d'état, `/var/lib/opencloud/agent` par défaut |
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
lance `opencloud agent -state
/var/lib/opencloud/agent`, sans `-server` ni jeton : l'agent relit son
identité (`identity loaded`) et se connecte. Il tourne en root : il lit
chaque volume monté et la socket Docker.

Une coupure de réseau ou un redémarrage de `serve` ne l'arrête pas : il se
reconnecte seul, avec un délai de 1 à 60 s. Un défi perdu ou expiré
(`challenge_expired`, par exemple si `serve` a redémarré entre le défi et
l'ouverture du flux) ne l'arrête pas non plus : il en redemande un. Il
s'arrête, code de sortie 2, quand openCloud refuse son identité (machine
retirée, signature qui n'est plus celle de la clé enrôlée, `bad_proof`, ou
agent révoqué, `revoked`) :
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
| Couper un agent sans perdre la machine (machine compromise, clé volée) | Machines → la machine → **Révoquer l'agent**. Son flux tombe, sa prochaine preuve est refusée (`revoked`) et il s'arrête, code 2 ; les jetons de ré-enrôlement en attente sont annulés. La machine, son historique, ses services restent ; elle passe hors ligne sans être déclarée perdue. Pour la rétablir : **Ré-enrôler**, sur une machine saine. Ce que l'agent détenait (les `.env` des services de la machine) reste à changer à la main |
| Retirer une machine | Machines → la machine → **Retirer**. La confirmation montre d'abord ce que le retrait emporte (mesures, conteneurs…) et ce qui le retient : services déclarés, sondes qu'elle lance, tâches rattachées, composants de la page de statut. Tant qu'il en reste, le bouton est fermé ; supprimez-les ou détachez-les (changer la machine d'une sonde, retirer la machine d'une tâche ou d'un composant). Son historique est effacé ; l'agent s'arrête en disant « openCloud ne reconnaît plus cette machine » |

Tant que `identity.json` existe, l'agent l'utilise et ne lit jamais l'entrée
standard : il ne bloque pas sous systemd. Le jeton ne passe jamais en
argument : `-token` est refusé, il serait visible de tous les comptes via `ps`.

## Mettre à jour

Il n'y a ni paquet ni dépôt : on remplace le binaire. Les migrations de la
base s'appliquent **seules au démarrage**, dans l'ordre, chacune dans sa
transaction, notées dans `schema_migrations`. Pas de commande à lancer, pas
de retour arrière : **sauvegardez avant**.

```sh
# Sur le poste de build
git pull && make build

# Sur le serveur : sauvegarder, puis remplacer et redémarrer
runuser -u opencloud -- sqlite3 /var/lib/opencloud/opencloud.db ".backup '/var/lib/opencloud/avant-maj.db'"
install -m 0755 opencloud /usr/local/bin/opencloud
systemctl restart opencloud

# Puis sur chaque machine
install -m 0755 opencloud /usr/local/bin/opencloud
systemctl restart opencloud-agent
```

`install` remplace le binaire pendant que le service tourne (vérifié ici) ;
le nouveau prend effet au redémarrage.

**Le niveau label demande des agents à jour ; mettez à jour le serveur d'abord, puis les agents.**
Un agent plus ancien ne transmet pas les labels : un conteneur étiqueté reste rattaché par son projet Compose ou
par son nom (ou non déclaré) tant que son agent n'est pas mis à jour ; un
agent à jour contre un serveur plus ancien fonctionne, le serveur ignore ce
qu'il ne connaît pas. À la mise à jour de l'agent, un conteneur étiqueté passe
au niveau « label » au rapport suivant ; un label faux ou mal formé le rend
« label orphelin », sans repli.

**Le premier démarrage d'une version qui relie l'annuaire** (migration
`024_directory_links.sql`) repointe sur l'annuaire ce qui visait un
conteneur : les sondes, les membres des composants de la page de statut et la
politique de mise à jour (suivre, épingler, exclure). Pour chaque conteneur qui
portait un de ces liens, `serve` crée un service dans l'annuaire, avec tout son
projet Compose, marqué « à vérifier » : l'onglet **Annuaire** de la page
Services les liste, et une ligne `declared services to review` au journal en
donne le nombre. Seuls les conteneurs liés ou qui tournent y deviennent des
unités : un conteneur arrêté que rien ne vise (l'`init` d'un projet) reste
« non déclaré », visible dans l'écart. **Après la montée, vérifiez vos services
à valider et vos composants de statut**, puis **Valider** chaque service. L'historique des sondes,
leurs alertes et les membres des composants sont gardés ; la migration compte
ce qui devait survivre et, au moindre écart, ne change rien et `serve` refuse de
démarrer (le message dit lequel). Recréer un conteneur ne casse plus ces liens.
**Pas de retour arrière par le binaire seul** : rien n'est détruit, mais
l'ancien binaire ne marche pas sur cette base. Il démarre, puis ne suit plus
les conteneurs : son inventaire écrit une colonne (`update_policy`) que la
montée a retirée de `services`, donc chaque rapport échoue ; créer une sonde
sur un conteneur échoue aussi, la sonde visant désormais un service de
l'annuaire. La 025 renomme en plus les tables du conteneur observé (`services`
devient `containers`, `service_transitions` `container_events`, et de même
les mesures et les réseaux) : un binaire d'avant elle ne les retrouve plus.
Pour revenir en arrière, remettez l'ancien binaire **et** la copie
`avant-maj.db` faite avant la mise à jour. La sauvegarde ne change pas.

**La version qui pose les alertes sur l'annuaire** (migration
`032_alerts_directory.sql`) change deux choses que vous voyez. **Une
maintenance de la page de statut ne tait plus les alertes** : c'est ce que
vous dites au public ; pour taire, posez un silence (Alertes → Silences),
qui peut maintenant commencer à une heure dite. À la montée, chaque
maintenance pas encore terminée devient un silence par objet de ses
composants, sur la même fenêtre : rien ne change pour celles en cours. Les
silences existants sont repris (celui d'un volume tait désormais le disque de
toute sa machine). Et **une livraison en échec n'est plus perdue** : elle est
rejouée, les essais s'espaçant jusqu'à une heure, pendant 24 h. Le journal d'audit écrit
`mute.create` et `mute.delete` là où il écrivait `silence.*`. Un binaire
d'avant ne retrouve plus `alert_silences` : même retour arrière que plus haut.

**L'ordre : le serveur d'abord, puis les agents.** Ce que le code garantit :

| Situation | Effet |
| --- | --- |
| Agent plus ancien que le serveur | Accepté. Le serveur ignore les sections du signal que l'agent n'envoie pas, et lui répond dans l'ancien format de refus |
| Commande du serveur inconnue de l'agent | Ignorée, une ligne `unknown command` au journal de l'agent |
| Champ JSON inconnu dans ce que l'agent envoie | Ignoré par le serveur |
| Serveur redémarré | Les agents se reconnectent seuls ; l'interface et les flux aussi |
| Identité | Gardée : une mise à jour ne demande jamais de ré-enrôler |

Il n'y a **pas de négociation de version** : rien n'empêche un agent plus
récent que le serveur, mais ce cas n'est pas garanti. La colonne Agent de la
page Machines affiche la version de chaque agent ; tant qu'elle reste
`v0.0.1`, elle ne distingue pas deux builds. `opencloud version` sur la
machine donne le commit.

## Sauvegarder et restaurer

### Quoi

| Où | Quoi | Perte |
| --- | --- | --- |
| Serveur, `state_dir` | `opencloud.db` (avec `-wal` et `-shm` tant qu'il tourne) | Tout : machines, historique, sondes, tâches, alertes, page de statut, réglages de l'interface (langue, seuils du disque, relais d'e-mails mot de passe compris, fin de support), MCP et authentification, et le journal d'audit (Administration → Journal : qui a fait quoi, un an). `opencloud auth disable` et `opencloud secrets forget` s'y inscrivent sous le compte qui les lance |
| Serveur, `state_dir` | `secret.key` | La clé des secrets de la base : sans elle, la base garde ses données mais perd le mot de passe du relais, l'URL et le secret des canaux, les URL de ping (voir [Clé perdue](#clé-perdue)) |
| Serveur | `/etc/opencloud/config.toml` | La configuration |
| Chaque machine | `/var/lib/opencloud/agent/identity.json` | Sa clé : il faudra ré-enrôler |

**La base chiffre ses secrets** : les jetons de ping, l'URL et le secret des
canaux d'alerte (un webhook Discord, Slack ou ntfy porte son jeton dans
l'URL) et le mot de passe du relais d'e-mails sont chiffrés (AES-256-GCM) avec
la clé de `secret.key`. Les jetons d'enrôlement, les jetons MCP et le mot de
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
remplace pas : copiez-la une fois dans un gestionnaire de mots de passe ou un
coffre, **pas à côté des sauvegardes de la base** (qui perdraient leur
intérêt). La sauvegarde à chaud ci-dessous ne la contient pas ; la sauvegarde
à froid (`tar` du dossier) l'inclut, protégez-la comme un secret.

```sh
cat /var/lib/opencloud/secret.key
```

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
rend les deux ensemble.

Retirez les `-wal` et `-shm` de l'ancienne base avant de poser la copie :
SQLite les rejouerait sur elle. Une base plus ancienne que le binaire est
migrée au démarrage.

Les agents gardent leur identité : ceux que la copie connaît se
reconnectent seuls. Une machine enrôlée après la sauvegarde est refusée
(« openCloud ne reconnaît plus cette machine ») : ré-enrôlez-la.

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

## Désinstaller

openCloud n'a rien modifié ailleurs : il n'écrit jamais dans Docker ni sur
une machine.

**Une machine** :

```sh
# D'abord, dans l'interface : Machines → la machine → Retirer
systemctl disable --now opencloud-agent
rm /etc/systemd/system/opencloud-agent.service && systemctl daemon-reload
rm /usr/local/bin/opencloud
rm -r /var/lib/opencloud/agent
```

**Le serveur** (après une dernière sauvegarde si besoin) :

```sh
systemctl disable --now opencloud
rm /etc/systemd/system/opencloud.service && systemctl daemon-reload
rm /usr/local/bin/opencloud
rm -r /etc/opencloud /var/lib/opencloud
userdel opencloud
# et le fichier openCloud de la configuration dynamique de Traefik
```

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
| Agent : « Cette machine n'est pas enrôlée. Lancez la commande d'installation donnée par openCloud. » | Service lancé avant l'enrôlement, ou `-state` différent | Faire le premier lancement avec le même `-state` |
| Agent : « openCloud ne reconnaît plus cette machine : elle a été retirée ou ré-enrôlée ailleurs. » | Machine retirée, ou restauration d'une base plus ancienne | Ré-enrôler |
| Agent : « Certificat refusé : ce n'est pas celui dont l'empreinte a été épinglée… » | Le certificat d'openCloud a changé, ou quelqu'un s'interpose | Vérifier l'empreinte, puis ré-enrôler avec la nouvelle |
| Agent : `tls: failed to verify certificate` | Certificat auto-signé sans `-pin` | `-pin`, ou un certificat reconnu |
| Machine openCloud : « L'agent n'a pas le droit de lire la socket Docker de cette machine. » | Le compte du service n'est pas dans `docker` | `usermod -aG docker opencloud`, `SupplementaryGroups=docker`, redémarrer |
| « Docker n'est pas présent sur cette machine. » alors qu'il tourne | `docker_socket` ou `-docker-socket` ne vise pas la bonne socket | Corriger le chemin |
| `serve: listen tcp 172.17.0.1:8080: bind: cannot assign requested address` | `docker0` pas encore là au démarrage | L'unité redémarre seule ; vérifier `After=docker.service` |
| `serve: listen tcp …: bind: address already in use` | Un autre processus tient le port | `ss -ltnp` ; changer `listen` |
| Le serveur ou `opencloud mcp` refuse de démarrer : « opencloud.db contient des secrets chiffrés mais secret.key est absent de state_dir… » | La clé n'a pas été restaurée avec la base | La poser (voir Restaurer) ; perdue : voir [Clé perdue](#clé-perdue) |
| « secret.key est lisible par d'autres comptes » | Le fichier a un mode plus large que `0600` | `chmod 600 /var/lib/opencloud/secret.key`, propriétaire le compte du service |
| `opencloud mcp` : « secret.key absent : démarrez serve une fois, ou restaurez la clé » | `mcp` ne crée jamais la clé | Démarrer le serveur une fois, ou restaurer la clé |
| Le navigateur redemande le mot de passe à chaque fois, ou `429` | Mauvais identifiants, puis limite des échecs ; ou file de vérification pleine (des tentatives anonymes en cours) | Attendre `Retry-After` et réessayer ; mot de passe perdu : `opencloud auth disable` |
| `sudo: command not found` | Debian minimal, installé sans `sudo` | En root, retirer `sudo` ; sous le compte du service, `runuser -u opencloud -- …` |
| Tous les utilisateurs bloqués ensemble en `429` | `trusted_proxies` absent : tout le monde a l'adresse de Traefik | Déclarer Traefik dans `trusted_proxies` |
