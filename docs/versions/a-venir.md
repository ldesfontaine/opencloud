# À venir

Les changements de la prochaine version, pas encore publiée. Cette page est
renommée `<tag>.md` à la publication, et son entrée passe dans le tableau de
[`README.md`](README.md).

## L'essentiel

- **Un serveur openCloud compromis ne peut plus exfiltrer de données par les sauvegardes** (lot 1 de l'étude « compromission du serveur », #487) : l'agent n'exécute `backup.produce` et `restic.provision` que sur des **ancres de confiance** que l'administrateur de la machine pose lui-même.
- **La mémoire d'une machine en ZFS ne compte plus le cache ZFS** (#328) : un Pi de sauvegarde à 70 % de mémoire « utilisée » dont 9,9 Go d'ARC affiche désormais ce qu'il utilise vraiment, et le cache ZFS apparaît à part sur la fiche.
- **Deux migrations** (`036`, `038`) : **sauvegarde de la base obligatoire** avant la mise à jour.
- **Le protocole de l'agent ne change pas** (version `2`) : un agent v0.6.0 reste connecté et continue de tout faire, mais **n'applique pas les ancres**.
- **Aucune clé de `config.toml` nouvelle.**

## Les ancres de confiance

| Ancre | Borne | Valeurs |
| --- | --- | --- |
| `backup.recipient` | la clé age pour laquelle `backup.produce` chiffre | 1 à 2 |
| `backup.guest` | les VM et conteneurs que `proxmox-vzdump` copie | 1 à 32 |
| `restic.target-cert` | les certificats de cibles restic que `restic.provision` accepte | 1 à 4 |

- **Refus par défaut** : sans ancre posée, l'ordre est refusé (`anchor_missing:<ancre>`), avec la commande à lancer sur la machine.
- **Se posent** au premier lancement (un drapeau par ancre, à côté de `-rights`) ou par `opencloud agent trust list | add | remove`, **en root**. L'agent les relit à chaque ordre : pas de redémarrage.
- **L'écran** : la ligne d'installation et la fenêtre « Ré-enrôler » les demandent (la clé du trousseau est proposée) ; la fiche, onglet Agent, dit « posées », « aucune posée » ou « illisibles ».
- **Le journal local de l'agent** (journald) porte, par ordre, le run, le genre, le rang, le SHA-256 du corps signé, l'empreinte de la clé qui l'a signé et les paramètres non secrets.

## Ce que ça coûte

- Chaque VM à sauvegarder, chaque clé de trousseau changée, chaque certificat de cible renouvelé demande **un geste de root sur la machine** avant que l'ordre passe.
- **Une machine qui porte `backup.produce` ou `restic.produce` voit ces ordres refusés une fois son agent mis à jour**, tant qu'aucune ancre n'est posée. Au 4 novembre 2026, aucune machine connue ne porte ces droits (relevé par l'API) : rien ne casse.
- Un agent v0.6.0 n'est pas protégé tant qu'il n'est pas mis à jour ; la fiche le dit.

## Ce qui n'est pas couvert (lot 2)

Les clés d'opérateur, la co-signature, le verrouillage de la clé du serveur, les plafonds de gestes, les secrets restic et la clé OPNsense hors du serveur. Un serveur compromis qui détient les mots de passe des dépôts lit toujours une sauvegarde restic qui passe par son relais. Un accès root à la machine de l'agent réécrit ses ancres.

## Le cache ZFS dans la mémoire (#328)

- **Avant** : utilisée = `MemTotal − MemAvailable`. Le noyau ne compte pas l'ARC ZFS dans `MemAvailable` : sur un Pi de 16 Go, 11 Go « utilisés » dont 9,9 Go (9,2 Gio) d'ARC, pour 29 Mo de plus gros processus.
- **Maintenant** : l'agent lit `size` et `c_min` dans `/proc/spl/kstat/zfs/arcstats` et retranche `size − c_min`. `mem_used` porte la valeur corrigée (les seuils d'alerte la lisent) ; `zfs_cache` porte l'ARC entier.
- **L'écran** : la tuile mémoire de la fiche dit « cache ZFS 9,2 Gio » à côté du pourcentage. Sans ZFS, ou avec un agent ancien : rien de plus, comme avant.
- **Le protocole de l'agent reste en version `2`** (champ optionnel). Un agent v0.6.0 ne corrige rien : sa mémoire reste fausse sur une machine en ZFS jusqu'à sa mise à jour.
- **Ce que ça coûte** : l'ARC est retranché en entier au-dessus de son plancher, même s'il ne se vide pas d'un coup ; sous forte pression la valeur peut être un peu basse. L'historique d'avant la mise à jour garde l'ancienne valeur ; la taille du cache n'est gardée que 48 h (le brut), pas dans les moyennes.
