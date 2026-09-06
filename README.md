# openCloud

Un outil web auto-hébergé pour administrer les machines Linux d'une
infrastructure — publier des sites, obtenir des certificats, sauvegarder,
observer — sans que ces machines dépendent de lui pour fonctionner.

## Ce qu'il fait

- **Publier** : un hôte virtuel Traefik pour un domaine, sur la bonne machine.
- **Certifier** : un certificat Let's Encrypt en DNS-01 via Cloudflare.
- **Sauvegarder** : localement par minuterie, à froid sur un disque chiffré.
- **Observer** : santé des machines, journaux, alertes, bannissements.

Tout ce qu'il fait se fait aussi à la main, au même endroit, de la même
façon. Il ne réconcilie rien : la machine fait foi.

## La pile

| | |
|---|---|
| Programme | Go, un seul binaire, interface HTML rendue côté serveur (HTMX), SQLite |
| Machines | Debian ou Ubuntu, pilotées par SSH, sans agent |
| Sur chaque machine | Traefik, CrowdSec, un collecteur de métriques, Docker |
| Fichiers | `/srv/workspace/<env>/<service>` et `/srv/data` ; l'état d'openCloud dans `/var/lib/opencloud` |

## Déployer

*Pas encore de release.* Le déploiement prévu, sur Debian ou Ubuntu :

```bash
sudo apt install ./opencloud_<version>_amd64.deb
```

Un seul binaire qui embarque l'interface, les migrations et les scripts ;
le paquet pose l'unité systemd, `/etc/opencloud/config.toml` et
`/var/lib/opencloud`. Puis :

1. Ouvrir l'interface, changer le mot de passe.
2. Pour chaque machine à gérer, jouer la commande d'enrôlement générée par
   l'interface. Elle crée l'utilisateur `opencloud`, pose sa clé, et affiche
   l'empreinte de la machine.

Mise à jour : `apt install` de la version suivante, ou `sudo opencloud
self-update` — en place, sans réinstaller (`docs/projet/20-installation-et-mise-a-jour.md`).

## Utiliser

Une machine enrôlée reçoit des **actions** : poser le socle, installer le
proxy, créer un hôte virtuel, demander un certificat, déployer un service,
sauvegarder, mettre à jour, redémarrer. Chaque action montre ce qu'elle va
faire avant, et ce qu'elle a fait après.

Un service est un dossier avec un `compose.yaml` et un `Makefile` aux cibles
standard (`up`, `down`, `update`, `backup`…). openCloud appelle ces cibles ;
à la main, on appelle les mêmes.

## Développer

```bash
make run
```

Construit le binaire et le lance avec `dev/config.toml`, un fichier hors git
à créer une fois. Rien n'est installé sur la machine : l'état vit dans
`dev/state`.

```toml
listen = "127.0.0.1:8080"
state_dir = "state"             # relatif au fichier de configuration
allow_default_password = true   # jamais en production
```

Puis ouvrir `http://localhost:8080` et se connecter avec `admin` /
`opencloud`. `make ci` joue les mêmes vérifications que la CI. Tester le
paquet `.deb` ou l'unité systemd se fait dans un conteneur ou une VM, jamais
sur la machine de développement.

## Documentation

Le cadrage complet est dans [`docs/projet/`](docs/projet/README.md).

## Licence

Apache 2.0 — voir [`LICENSE`](LICENSE).
