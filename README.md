# openCloud

[English](README.en.md) · Contribuer : voir [AGENTS.md](AGENTS.md) et [docs/projet/](docs/projet/)

[![ci](https://github.com/ldesfontaine/opencloud/actions/workflows/ci.yml/badge.svg?branch=dev)](https://github.com/ldesfontaine/opencloud/actions/workflows/ci.yml)

**openCloud est une supervision auto-hébergée** : vos machines, leurs
conteneurs Docker en lecture seule, des sondes HTTP/TCP et leurs certificats,
vos tâches planifiées par ping, des alertes par webhook signé, une page de
statut publique, les mises à jour d'images détectées, et un serveur MCP pour
les agents IA. Un seul binaire Go, avec SQLite et l'interface web embarqués.

> **Maturité.** Version `v0.0.1`, pré-version. Les binaires Linux amd64 et
> arm64 sont dans la [release v0.0.1](https://github.com/ldesfontaine/opencloud/releases/tag/v0.0.1),
> ou se compilent. L'interface est **ouverte par défaut** : activez
> l'authentification avant de l'exposer hors de la machine.

![Vue d'ensemble d'openCloud, avec des machines d'exemple](docs/images/vue-ensemble.png)

## Ce que ça fait, ce que ça ne fait pas

| Fait | Ne fait pas |
| --- | --- |
| Mesure chaque machine toutes les 10 s : processeur, mémoire, disques, réseau | Ne lance, n'arrête, ne redémarre rien sur une machine |
| Suit les conteneurs Docker : état, santé, redémarrages, journaux, réseaux | N'écrit jamais dans Docker : la socket est lue, aucun verbe d'écriture |
| Sonde des URL et des ports, juge les certificats et leur échéance | Ne renouvelle pas un certificat |
| Surveille un cron ou une sauvegarde par une URL de ping | N'exécute pas la tâche |
| Compare chaque image à son registre et fabrique la commande de mise à jour | N'applique pas la mise à jour : vous la jouez |
| Dit si la version d'un service est encore maintenue, jusqu'à quand, et vers quelles branches monter, d'après endoflife.date | Coupé par défaut : rien ne sort tant que vous ne l'activez pas. Ne cherche pas de failles (CVE) |
| Envoie les alertes en webhook signé (Discord, Slack, Mattermost, ntfy, JSON), par e-mail via votre relais SMTP, ou dans un salon Matrix | Pas de SMS |
| Publie une page de statut sous `/statut` | Pas de comptes multiples ni de second facteur en propre : derrière un mandataire (Authelia), le mode `forward` les délègue à votre SSO |
| Sert MCP : 16 outils de lecture, 9 écritures que l'interface a déjà | Aucune action sur une machine par MCP, aucun secret rendu |

openCloud **observe, il n'agit jamais sur une machine**. L'interface existe
en français et en anglais (sélecteur FR/EN en bas de la barre latérale).

## Comment c'est fait

| Pièce | Rôle |
| --- | --- |
| `opencloud serve` | Sur la machine openCloud : l'interface, l'API, la base. Il supervise aussi sa propre machine, sans agent à part |
| `opencloud agent` | Sur chaque autre machine : s'enrôle une fois par jeton, puis tient un flux HTTPS vers le serveur. Clé Ed25519, aucune connexion entrante |
| Un proxy (Traefik) | Termine TLS devant `serve`, qui écoute en HTTP sur une adresse locale |

Linux seulement (l'agent lit `/proc`). Vérifié sur Linux amd64 ; arm64 se
compile par compilation croisée, sans test sur une vraie machine arm64 à ce jour.

## Démarrage rapide

Pour essayer sur votre poste. L'installation sur un serveur est dans
[docs/installation.md](docs/installation.md).

Il faut Go 1.26.9 ou plus, Node.js 20.19 ou plus, `make` et `git`.

```sh
git clone https://github.com/ldesfontaine/opencloud.git
cd opencloud
make build                      # compile le front puis le binaire : bin/opencloud
```

Une configuration minimale :

```sh
mkdir -p dev/state
cat > dev/config.toml <<EOF
listen = "127.0.0.1:8080"
state_dir = "$PWD/dev/state"
EOF
./bin/opencloud serve -config dev/config.toml
```

`make run` fait ces deux étapes à votre place. Ouvrez
<http://127.0.0.1:8080>.

**Activez l'authentification** : Paramètres → Accès à l'interface. Ou par
l'API, tant qu'elle est ouverte :

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"une phrase de 12 caractères ou plus"}' \
  http://127.0.0.1:8080/api/auth/actions/enable
```

**Ajoutez une machine** : Machines → Ajouter une machine, donnez-lui un nom.
openCloud affiche une commande à coller sur la machine, en root :

```sh
sudo opencloud agent -server https://opencloud.example.org -token oc_…
```

Le jeton sert une fois et expire en 24 h. N'enrôlez pas d'agent sur la
machine openCloud : elle est déjà supervisée, un agent dessus doublerait ses
services et ses alertes. Pour essayer, prenez une autre machine ou une VM ;
l'agent exige HTTPS vers openCloud, sauf `-allow-plain` sur un réseau déjà
chiffré (WireGuard). La suite (proxy, service, `-pin`) est dans
[docs/installation.md](docs/installation.md#ajouter-une-machine).

Mot de passe perdu : `opencloud auth disable -config dev/config.toml`.

## Documentation

| Document | Pour |
| --- | --- |
| [docs/installation.md](docs/installation.md) | Installer sur un VPS Debian avec Traefik, ajouter une machine, mettre à jour, sauvegarder, désinstaller |
| [docs/installation/](docs/installation/) | Fichiers d'exemple : `config.toml` commenté, unités systemd, configuration Traefik |
| [docs/projet/22-fonctionnement.md](docs/projet/22-fonctionnement.md) | Le fonctionnement détaillé : flux, chiffrement, données stockées, limites connues |
| [AGENTS.md](AGENTS.md), [docs/projet/](docs/projet/) | Contribuer : méthode, conventions de code, direction artistique |

## Licence

[Apache 2.0](LICENSE).
