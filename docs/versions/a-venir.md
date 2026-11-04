# À venir

Les changements de la prochaine version, pas encore publiée. Cette page est
renommée `<tag>.md` à la publication, et son entrée passe dans le tableau de
[`README.md`](README.md).

## L'essentiel

- **Un serveur openCloud compromis ne peut plus exfiltrer de données par les sauvegardes** (lot 1 de l'étude « compromission du serveur », #487) : l'agent n'exécute `backup.produce` et `restic.provision` que sur des **ancres de confiance** que l'administrateur de la machine pose lui-même.
- **Une migration** (`036`) : **sauvegarde de la base obligatoire** avant la mise à jour.
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
