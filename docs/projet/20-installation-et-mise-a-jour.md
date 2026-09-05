# Installation et mise à jour

Comment openCloud se pose sur une machine, et comment il se met à jour sans
être réinstallé. Fondé sur ce que font les binaires Go comparables (Caddy,
Headscale, Gitea, Beszel, SysWarden) et sur ce qui a cassé chez les autres.

## Un binaire qui embarque tout

Le front HTML, les fichiers statiques, les migrations SQLite, les scripts
d'actions et les gabarits sont **compilés dans le binaire** (`embed.FS`).
Déployer, c'est poser un fichier. Aucun runtime, aucune dépendance.

## Installer : un paquet `.deb`

```bash
sudo apt install ./opencloud_0.1.0_amd64.deb
```

| Le paquet pose | Où |
|---|---|
| Le binaire | `/opt/opencloud/bin/opencloud` — **le nom ne change jamais** entre versions |
| La configuration | `/etc/opencloud/config.toml` — *conffile* : dpkg ne l'écrase pas à la mise à jour |
| L'état | `/var/lib/opencloud/` — `StateDirectory=` de l'unité, préservé par dpkg |
| L'utilisateur système et l'unité durcie | `opencloud`, `opencloud.service` |

Produit par `nfpm` depuis `make release`, avec la somme SHA-256 publiée dans
la release GitHub et l'attestation de provenance. Pas de `curl | sh`.

Un dépôt apt signé viendra si la cadence de publication le justifie ; tant
qu'il n'existe pas, un `.deb` téléchargé et vérifié suffit.

**Pourquoi un paquet et pas un script** : dpkg sait déjà préserver `/etc`,
laisser `/var/lib` en place, créer l'utilisateur, activer l'unité, et **mettre
à jour en place** avec `apt install` d'une version plus récente. Un script
refait tout ça à la main, mal — c'est là que Coolify et Cloudron ont cassé.

## Mettre à jour : deux chemins, un seul mécanisme

| Chemin | Geste |
|---|---|
| Par le paquet | `sudo apt install ./opencloud_0.2.0_amd64.deb` |
| Par le binaire | `sudo opencloud self-update` — ou le bouton « mettre à jour » de l'interface, qui l'appelle |

Dans les deux cas, ce qui se passe, **dans cet ordre** :

1. **Télécharger** la release, **vérifier** la somme SHA-256 et l'attestation.
   Refus si l'une manque.
2. **Refuser un saut de version mineure** : `0.1 → 0.3` est refusé, le chemin
   est séquentiel *(Headscale)*.
3. **Garder l'ancien binaire** à côté : `opencloud.prev`.
4. **Remplacer par écriture atomique** — fichier temporaire puis `rename`,
   jamais d'écrasement du fichier en cours d'exécution *(`text file busy`)*.
5. **Redémarrer l'unité.**
6. **Au démarrage, sauvegarder la base**, puis appliquer les migrations
   embarquées, numérotées, jamais rejouées. **Si la sauvegarde échoue, la
   migration n'a pas lieu** et l'ancienne version reste servie *(Cloudron)*.

Retour arrière : remettre `opencloud.prev` et la sauvegarde de base prise
avant migration. Ça se fait à la main, en deux commandes que l'interface
affiche.

## Ce qu'on ne fait pas

- **Pas de mise à jour automatique silencieuse.** L'interface signale la
  version disponible ; le geste est explicite. *(Netdata l'active par défaut,
  Coolify l'a payé.)*
- **Jamais par la file d'actions** : la mise à jour n'est pas une action.
- **Le bouton se désactive au premier clic** — un double-clic sur Coolify a
  lancé deux mises à jour en parallèle et perdu la clé de chiffrement.
- **Pas de migration pendant qu'un autre processus peut redémarrer le
  service** : `apt-daily-upgrade` a coupé une migration Cloudron au milieu.
  L'unité pose un verrou de migration, et le dit s'il reste.

## Piège connu à ne pas reproduire

`ProtectSystem=strict` dans l'unité **verrouille SQLite** si le dossier d'état
n'est pas déclaré en écriture *(Headscale #1274)*. L'unité openCloud déclare
`StateDirectory=opencloud` et `ReadWritePaths=` explicites — durcie, mais
capable d'écrire là où elle doit.
