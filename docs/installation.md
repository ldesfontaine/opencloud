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
9. [Ajouter une machine](#ajouter-une-machine)
10. [Mettre à jour](#mettre-à-jour)
11. [Sauvegarder et restaurer](#sauvegarder-et-restaurer)
12. [Désinstaller](#désinstaller)
13. [Dépannage](#dépannage)

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
| Go | 1.26.9 ou plus (`go.mod`) | Compiler, sur le poste de build seulement |
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
| `opencloud auth status\|disable -config <chemin>` | L'état de l'authentification, ou la couper (mot de passe perdu) |
| `opencloud mcp -config <chemin>` | MCP sur stdin/stdout pour un client IA local, en lecture seule |
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
d'état et la version. Tant que l'authentification n'est pas activée, une
ligne `WARN` le rappelle, en majuscules si `listen` n'est pas sur la boucle
locale.

**Root plutôt qu'un compte dédié ?** Possible : retirez `User=`, `Group=` et
`SupplementaryGroups=`. Le compte dédié limite ce que le processus touche
en dehors de Docker.

**Les commandes `auth` et `mcp` se lancent sous le compte du service** :

```sh
runuser -u opencloud -- opencloud auth status -config /etc/opencloud/config.toml
```

Lancées en root, elles peuvent créer `opencloud.db-wal` au nom de root si
le service est arrêté, et `serve` ne pourrait plus écrire dans sa base.

## Configuration

Fichier TOML, par défaut `/etc/opencloud/config.toml`. Le fichier complet et
commenté : [`installation/config.toml`](installation/config.toml).

| Clé | Défaut | Rôle |
| --- | --- | --- |
| `listen` | `127.0.0.1:8080` | Adresse d'écoute, `hôte:port`, en HTTP. Le seul port d'openCloud |
| `state_dir` | `/var/lib/opencloud` | La base `opencloud.db` (et `-wal`, `-shm`) et `settings.toml`. Créé en `0700` s'il manque |
| `public_url` | vide | L'adresse publique, par exemple `https://opencloud.example.org`. Voir ci-dessous |
| `trusted_proxies` | `[]` | Les mandataires dont les en-têtes `X-Forwarded-*` sont crus, en CIDR |
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
  attaquant peut bloquer l'opérateur ;
- sans `public_url`, les adresses construites ne connaissent pas le nom
  public.

Déclarez l'adresse d'où Traefik se connecte, **seule** : `127.0.0.1/32` en
mode host, son IP fixe en `/32` s'il est en conteneur. Jamais un
sous-réseau : chaque pair de la liste est cru sur parole, et un conteneur
voisin écrirait `X-Forwarded-For` lui-même. Un préfixe plus large qu'une
adresse fait un avertissement au démarrage. `X-Forwarded-For` se lit de la
droite : ce qu'un client écrit lui-même à gauche ne compte pas.

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
| Traefik en mode host (`network_mode: host`, ou paquet sur l'hôte) | `127.0.0.1:8080` | `http://127.0.0.1:8080` | `["127.0.0.1/32", "::1/128"]` |
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
  conteneurs de la machine, pas seulement Traefik. Activez
  l'authentification. Le port n'est pas publié sur l'interface publique du
  VPS, mais un pare-feu qui n'accepte que 22, 80 et 443 en entrée reste une
  bonne idée.

### Les flux en direct

`/agent/stream`, `/api/events`, `/api/services/{id}/logs/stream`,
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

L'interface est **ouverte par défaut** : qui atteint le port enrôle des
machines, lit les journaux de conteneurs et active MCP. Activez
l'authentification avant d'exposer openCloud. Deux modes : identifiant et
mot de passe (HTTP Basic, ci-dessous), ou
[un mandataire qui authentifie](#derrière-un-mandataire-qui-authentifie)
(Authelia).

**Dans l'interface** : Paramètres → Accès à l'interface. **Ou par l'API**,
tant qu'elle est ouverte :

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"une phrase de 12 caractères ou plus"}' \
  https://opencloud.example.org/api/auth/actions/enable
```

| Règle | Valeur |
| --- | --- |
| Mode | HTTP Basic, realm `openCloud`. Le navigateur demande les identifiants dans sa propre fenêtre |
| Identifiant | 1 à 64 caractères, sans `:` |
| Mot de passe | 12 caractères au moins, 72 octets au plus ; haché en bcrypt |
| Échecs | 10 d'un coup, puis un toutes les 6 s par adresse, `429` au-delà |
| Effet | Immédiat, sans redémarrage. Activer révoque tous les jetons MCP (jetons d'API et sessions OAuth) : recréez ceux qui servent |

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
# mode : none — interface ouverte, sans authentification
```

`serve` suit à la requête suivante, sans redémarrer. Réactivez ensuite sous
Paramètres.

### Derrière un mandataire qui authentifie

Quand un SSO garde déjà l'accès (Authelia en `forwardAuth` de Traefik), le
mode `forward` lui délègue tout : comptes, second facteur, déconnexion.
openCloud laisse passer une requête seulement si elle vient d'une adresse
de `trusted_proxies` **et** porte l'en-tête d'identité non vide
(`Remote-User` par défaut). Tout le reste répond `401`, sans fenêtre Basic :
pas de double connexion, et un conteneur voisin qui joint le port en direct
n'a rien.

| Prérequis | Pourquoi |
| --- | --- |
| `trusted_proxies` = l'adresse du mandataire **seul**, en `/32` (par exemple `["172.30.0.2/32"]`), pas le sous-réseau | Tout pair de cette liste est cru sur parole : avec un `/24`, un voisin du réseau écrirait l'en-tête lui-même |
| Le mandataire **écrase** l'en-tête envoyé par le client | Sinon un visiteur envoie `Remote-User: admin`. Traefik `forwardAuth` avec `authResponseHeaders: ["Remote-User"]` remplace la valeur du client par celle d'Authelia |
| Deux Traefik en chaîne : `trusted_proxies` vise celui qui joint openCloud, l'en-tête est nettoyé par celui qui appelle Authelia | Le second ne doit être joignable que par le premier : sinon on le contourne, en-tête forgé compris |
| Activer depuis une session **déjà authentifiée** par le mandataire | Garde-fou : openCloud refuse l'activation (`auth.forward_header_missing`) si la requête ne porte pas déjà l'en-tête depuis un pair de confiance. On ne peut pas s'enfermer dehors |

Le middleware Traefik côté Authelia :

```yaml
http:
  middlewares:
    authelia:
      forwardAuth:
        address: "http://authelia:9091/api/authz/forward-auth"
        authResponseHeaders: ["Remote-User", "Remote-Groups", "Remote-Email", "Remote-Name"]
```

**Activer** : ouvrez openCloud par son nom public, connectez-vous à
Authelia, puis Paramètres → Accès à l'interface → Mode « Mandataire
d'authentification ». Le corps de l'API est
`{"mode":"forward","header":"Remote-User"}` sur
`POST /api/auth/actions/enable`. Comme en Basic, activer révoque tous les
jetons MCP.

**Vérifier** que le contournement est fermé :

```sh
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

La contrepartie : tout repose sur `trusted_proxies` juste et sur un
mandataire qui nettoie l'en-tête. Un mandataire qui le laisse passer, ou un
`trusted_proxies` trop large, et n'importe qui devient l'opérateur.

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

Le relais vit dans `settings.toml`, **mot de passe compris, en clair** :
le fichier est en `0600` sous `state_dir`, comme la base ; l'interface et
l'API ne le réaffichent jamais.

## Ajouter une machine

### 1. Le jeton

Dans l'interface : Machines → **Ajouter une machine**, un nom en lettres
minuscules, chiffres et tirets. openCloud affiche une seule fois la commande
à coller :

```sh
sudo opencloud agent -server https://opencloud.example.org -token oc_…
```

Pas d'agent sur la machine openCloud elle-même : elle se supervise déjà.
Lancé là, l'agent est refusé (« cette machine est déjà supervisée par
openCloud lui-même ») et le jeton reste valable pour une autre machine.

Connecté en root sans `sudo` (Debian minimal) : collez la commande sans le
mot `sudo`.

Le jeton sert **une fois** et expire en **24 h**. L'adresse vient de
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
sudo opencloud agent -server https://opencloud.example.org -token oc_…
```

L'agent tire une clé Ed25519, s'enrôle, écrit
`/var/lib/opencloud/agent/identity.json` en `0600`, puis se connecte. Quand le
journal dit `"msg":"connected"`, arrêtez-le (Ctrl-C) : le service prend le
relais.

| Option | Retenue dans l'identité | Rôle |
| --- | --- | --- |
| `-server` | Oui | Adresse d'openCloud, premier lancement seulement |
| `-token` | — | Jeton d'enrôlement, premier lancement seulement |
| `-pin` | Oui | Empreinte SHA-256 du certificat d'openCloud, s'il est auto-signé |
| `-lang` | Oui | Langue des messages de l'agent, `fr` ou `en` |
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
/var/lib/opencloud/agent`, sans `-server` ni `-token` : l'agent relit son
identité (`identity loaded`) et se connecte. Il tourne en root : il lit
chaque volume monté et la socket Docker.

Une coupure de réseau ou un redémarrage de `serve` ne l'arrête pas : il se
reconnecte seul, avec un délai de 1 à 60 s. Il s'arrête, code de sortie 2,
quand openCloud refuse son identité (machine retirée) : l'unité ne le
relance pas (`RestartPreventExitStatus=2`) et `systemctl status` la montre
`failed`, code 2. C'est voulu : il faut un nouveau jeton.

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

sudo opencloud agent -server https://203.0.113.10 -token oc_… -pin <empreinte>
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
| Changer la clé d'une machine (identité perdue, certificat épinglé changé) | Machines → la machine → **Ré-enrôler** : un nouveau jeton. Sur la machine : arrêter le service, **supprimer `identity.json`**, relancer avec `-server` et `-token`. L'identifiant et l'historique sont gardés |
| Retirer une machine | Machines → la machine → **Retirer**. Son historique est effacé ; l'agent s'arrête en disant « openCloud ne reconnaît plus cette machine » |

Tant que `identity.json` existe, l'agent l'utilise et **ignore** `-token`.

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
| Serveur, `state_dir` | `opencloud.db` (avec `-wal` et `-shm` tant qu'il tourne) | Tout : machines, historique, sondes, tâches, alertes, page de statut, réglages MCP et d'authentification |
| Serveur, `state_dir` | `settings.toml` | La langue de l'interface, le titre et l'annonce de la page de statut, les seuils du disque, le relais d'e-mails (mot de passe compris) |
| Serveur | `/etc/opencloud/config.toml` | La configuration |
| Chaque machine | `/var/lib/opencloud/agent/identity.json` | Sa clé : il faudra ré-enrôler |

**La base contient des secrets en clair** : les jetons de ping et les
secrets de signature des canaux d'alerte ; `settings.toml`, le mot de passe
du relais d'e-mails. Protégez la sauvegarde (`0600`,
chiffrée hors de la machine). Les jetons d'enrôlement, les jetons MCP et le
mot de passe y sont hachés.

### Sauvegarder

La base est en mode WAL : copier `opencloud.db` seul pendant que `serve`
tourne donne une copie incomplète. Deux façons sûres.

**À chaud**, avec le client `sqlite3` (`apt install sqlite3`), sous le
compte du service :

```sh
runuser -u opencloud -- sqlite3 /var/lib/opencloud/opencloud.db \
  ".backup '/var/lib/opencloud/opencloud-$(date +%F).db'"
cp /var/lib/opencloud/settings.toml /var/lib/opencloud/settings-$(date +%F).toml
```

Vérifié avec le client `sqlite3` 3.45, service en marche : copie
cohérente, `PRAGMA integrity_check` à `ok`, puis restaurée comme ci-dessous.

**À froid** :

```sh
systemctl stop opencloud
tar -C /var/lib -czf /root/opencloud-$(date +%F).tar.gz opencloud
systemctl start opencloud
```

### Restaurer

```sh
systemctl stop opencloud
rm -f /var/lib/opencloud/opencloud.db-wal /var/lib/opencloud/opencloud.db-shm
install -m 0600 -o opencloud -g opencloud opencloud-2026-10-10.db /var/lib/opencloud/opencloud.db
install -m 0600 -o opencloud -g opencloud settings-2026-10-10.toml /var/lib/opencloud/settings.toml
systemctl start opencloud
```

Retirez les `-wal` et `-shm` de l'ancienne base avant de poser la copie :
SQLite les rejouerait sur elle. Une base plus ancienne que le binaire est
migrée au démarrage.

Les agents gardent leur identité : ceux que la copie connaît se
reconnectent seuls. Une machine enrôlée après la sauvegarde est refusée
(« openCloud ne reconnaît plus cette machine ») : ré-enrôlez-la.

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
| Agent : « Jeton refusé : il a expiré. » / « openCloud ne le connaît pas. » | Plus de 24 h, ou jeton annulé, ou mauvais serveur | Un nouveau jeton ; vérifier `-server` |
| Agent : « Cette machine n'est pas enrôlée. Lancez la commande d'installation donnée par openCloud, avec -server et -token. » | Service lancé avant l'enrôlement, ou `-state` différent | Faire le premier lancement avec le même `-state` |
| Agent : « openCloud ne reconnaît plus cette machine : elle a été retirée ou ré-enrôlée ailleurs. » | Machine retirée, ou restauration d'une base plus ancienne | Ré-enrôler |
| Agent : « Certificat refusé : ce n'est pas celui dont l'empreinte a été épinglée… » | Le certificat d'openCloud a changé, ou quelqu'un s'interpose | Vérifier l'empreinte, puis ré-enrôler avec la nouvelle |
| Agent : `tls: failed to verify certificate` | Certificat auto-signé sans `-pin` | `-pin`, ou un certificat reconnu |
| Machine openCloud : « L'agent n'a pas le droit de lire la socket Docker de cette machine. » | Le compte du service n'est pas dans `docker` | `usermod -aG docker opencloud`, `SupplementaryGroups=docker`, redémarrer |
| « Docker n'est pas présent sur cette machine. » alors qu'il tourne | `docker_socket` ou `-docker-socket` ne vise pas la bonne socket | Corriger le chemin |
| `serve: listen tcp 172.17.0.1:8080: bind: cannot assign requested address` | `docker0` pas encore là au démarrage | L'unité redémarre seule ; vérifier `After=docker.service` |
| `serve: listen tcp …: bind: address already in use` | Un autre processus tient le port | `ss -ltnp` ; changer `listen` |
| Le navigateur redemande le mot de passe à chaque fois, ou `429` | Mauvais identifiants, puis limite des échecs | Attendre ; mot de passe perdu : `opencloud auth disable` |
| `sudo: command not found` | Debian minimal, installé sans `sudo` | En root, retirer `sudo` ; sous le compte du service, `runuser -u opencloud -- …` |
| Tous les utilisateurs bloqués ensemble en `429` | `trusted_proxies` absent : tout le monde a l'adresse de Traefik | Déclarer Traefik dans `trusted_proxies` |
