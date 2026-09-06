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

Sur Debian ou Ubuntu, depuis la page des releases GitHub :

```bash
gh attestation verify opencloud_<version>_amd64.deb --repo ldesfontaine/opencloud
sudo apt install ./opencloud_<version>_amd64.deb
```

Un seul binaire qui embarque l'interface, les migrations et les scripts ;
le paquet pose l'unité systemd durcie, `/etc/opencloud/config.toml` et
`/var/lib/opencloud`, et démarre le service sur `127.0.0.1:8080`. Puis :

1. Ouvrir l'interface depuis la machine, changer le mot de passe, et
   seulement ensuite élargir `listen` dans la configuration.
2. Jouer `sudo opencloud enroll-local` une fois : la machine openCloud
   s'enrôle sur elle-même, par SSH vers `localhost` comme toute machine.
   Sans ce geste, l'interface tourne mais aucune action ne part.
3. Pour chaque autre machine, jouer la commande d'enrôlement générée par
   l'interface. Elle crée l'utilisateur `opencloud`, pose sa clé, et affiche
   l'empreinte de la machine.

Mise à jour : `apt install` de la version suivante, ou `sudo opencloud
self-update` — en place, sans réinstaller : somme et attestation vérifiées,
ancien binaire gardé en `.prev`, pas de saut de version mineure
(`docs/projet/20-installation-et-mise-a-jour.md`). `apt remove` garde l'état,
`apt purge` l'enlève ; ni l'un ni l'autre ne touche `/srv`.

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

Construit le binaire et le lance. Au premier lancement, `dev/config.toml`
est créé avec les valeurs par défaut ; il est à vous ensuite, hors git. Rien
n'est installé sur la machine : l'état vit dans `dev/state`.

Puis ouvrir `http://localhost:8080` et se connecter avec `admin` /
`opencloud`. Pour développer l'enrôlement, `make temoin-up` lance une Debian
jetable avec `sshd` sur `127.0.0.1:2222` : la déclarer dans l'interface, puis
jouer la commande générée par `docker exec -i opencloud-temoin bash -c '…'`.
`make temoin-down` la retire. `make ci` joue les mêmes vérifications que la CI. Le paquet
`.deb` et l'unité systemd se testent dans un conteneur Debian jetable,
`make package-test`, jamais sur la machine de développement ; `make release`
produit le paquet dans `dist/`.

## Documentation

Le cadrage complet est dans [`docs/projet/`](docs/projet/README.md).

## Licence

Apache 2.0 — voir [`LICENSE`](LICENSE).
