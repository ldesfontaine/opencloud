# À venir

Les changements de la prochaine version, pas encore publiée. Cette page est
renommée `<tag>.md` à la publication, et son entrée passe dans le tableau de
[`README.md`](README.md).

## L'essentiel

- **Un serveur openCloud compromis ne peut plus exfiltrer de données par les sauvegardes** (lot 1 de l'étude « compromission du serveur », #487) : l'agent n'exécute `backup.produce` et `restic.provision` que sur des **ancres de confiance** que l'administrateur de la machine pose lui-même.
- **La mémoire d'une machine en ZFS ne compte plus le cache ZFS** (#328) : un Pi de sauvegarde à 70 % de mémoire « utilisée » dont 9,9 Go d'ARC affiche désormais ce qu'il utilise vraiment, et le cache ZFS apparaît à part sur la fiche.
- **Une montée de version de service revient seule en arrière** (#493, lot A) : l'agent juge la santé du service pendant 5 minutes et remet la version précédente si elle ne tient pas. **Les données ne sont pas restaurées.** Voir plus bas.
- **Cinq migrations** (`036`, `037`, `038`, `039`, `040`) : **sauvegarde de la base obligatoire** avant la mise à jour.
- **Le protocole de l'agent ne change pas** (version `2`) : un agent v0.6.0 reste connecté et continue de tout faire, mais **n'applique pas les ancres**.
- **Des alertes de processeur et de mémoire**, des **seuils réglables par machine**, « **Lu / Non lu** » à la place d'« acquitter », et un **rappel** des alertes de danger restées sans lecture (#489) : voir plus bas.
- **Des catégories d'alertes par canal** (#494) : un canal choisit les familles qu'il reçoit (machines, services, tâches, sondes, sauvegardes, mises à jour). **Migration `040`** ; un canal existant n'a aucune catégorie et reçoit tout, comme avant.
- **Le parcours navigateur ne tombe plus sous charge** (#490) : il retrouve un choix de menu par son nom et attend l'état de la page, plus un délai. Aucun changement pour l'opérateur.
- **Rien de visible, mais une règle** (#498, lot 7 de la factorisation) : une logique n'a qu'une définition. Les tests attendent par `waittest.Until` (borne de 30 s au lieu de 2 ou 5 s), comparent les goldens par `goldentest.Compare` et partagent leurs petits gestes dans `testkit` ; des gardes `TestOneDefinition_*` refusent la copie. La documentation ne compte plus les ordres de l'agent.
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

## Les alertes de ressources (#489)

| Ressource | Attention | Danger | Fenêtre |
| --- | --- | --- | --- |
| Processeur | 95 % | 99 % | moyenne sur 15 min |
| Mémoire | 90 % | 95 % | moyenne sur 15 min |
| Disque | 85 % | 95 % | dernière lecture (inchangé) |

- **Une moyenne, pas un pic** : l'alerte s'ouvre quand toute la fenêtre est au-dessus du seuil, se résout 5 points plus bas. Une machine qui vient d'arriver, ou dont l'agent a été coupé, n'alerte pas tant que ses lectures ne couvrent pas la fenêtre.
- **Un réglage global** (Paramètres › Notifications) et **une surcharge par machine** (sa fiche, carte « Seuils des alertes » : « Plus de tolérance pour cette machine »). Une machine sans surcharge suit le global ; l'écran dit lequel s'applique. Le disque est inclus, et **les seuils du disque déjà réglés sont conservés** (migration `037`).
- **« Lu / Non lu »** remplace « acquitter » partout où l'opérateur le lit (« Marquer comme lue », « 3 non lues »). Le verbe d'API `alert.acknowledge` ne change pas.
- **Le rappel** : une alerte de **danger** ouverte, non lue et non tue depuis **12 h** est renvoyée **une seule fois** (« Toujours en cours »). Réglable sous Paramètres (1 à 168 h), **0 le coupe**. À la mise à jour, les alertes déjà ouvertes partent de ce moment : aucun rappel d'un coup.
- **La valeur « mémoire utilisée »** est celle que l'agent rapporte. Le correctif du cache ZFS (#328) la fera baisser sur les hôtes qui en ont ; les alertes de mémoire en profiteront sans autre changement.

### Ce que ça coûte

- Les jauges de processeur et de mémoire changent de couleur à **95 %** et **90 %** (comme leur alerte), non plus à 85 % : une machine à 88 % de mémoire n'a plus de jauge orange.
- Une alerte de processeur arrive **15 min après** le début de la charge, par choix : c'est ce qui évite le bruit des pics.
- Le seuil de retour n'est pas réglable (5 points sous l'attention).
- Les gestes de seuils sont d'interface : pas de verbe d'API ni de puce `MCP`.

## Les catégories d'un canal (#494)

- **Où** : Paramètres › Notifications › un canal › Réglages › « Alertes reçues ». Aucune case cochée : toutes les alertes, **y compris celles qui s'ajouteront**.
- **Contrat** : `categories` (liste de noms) dans l'API des canaux ; vide ou absent, toutes. Un nom inconnu est refusé (`channel.category_invalid`).
- **Ce que ça change** : un canal reçoit une alerte si sa gravité est atteinte **et** que sa famille est cochée. Le plafond de débit est par canal : un canal dédié aux mises à jour a son propre budget.
- **Ce que ça coûte** : un canal restreint ne reçoit **pas** un type d'alerte qui apparaît plus tard dans une autre famille que celles cochées ; l'écran de la liste montre les familles de chaque canal. Une catégorie décochée après coup n'empêche pas d'apprendre la fin d'une alerte déjà annoncée.

## Le retour de version d'un service (#493)

| Quoi | Réglage |
| --- | --- |
| Fenêtre pour devenir sain | 5 min (bornes de l'agent : 2 à 30 min) |
| Sans HEALTHCHECK : en marche sans redémarrage | 2 min (bornes : 1 à 10 min) |
| Retours tentés | un seul, jamais de boucle |
| Journal joint à la notification | 20 lignes, 2 Kio, valeurs de l'`.env` masquées ; une case par canal |

- **Qui fait quoi** : l'agent garde l'ancien `compose.yml`, juge la santé, revient seul ; le serveur remet la version d'avant dans l'annuaire, écrit `action.rolled_back` et ouvre l'alerte `service_rolled_back` (attention, danger si le retour a échoué aussi).
- **Sur l'écran** : la carte « Mise à jour de l'image » dit ce que la machine fera si la version ne tient pas, grise « Passer à » avec sa raison quand l'agent ne sait pas revenir, et garde « La version X a échoué » après un retour.
- **Nouvel ordre `docker.rollback`** (droit `docker.update`) : refusé hors fenêtre. Rien ne l'envoie encore : le veto des sondes liées est le lot E.
- **Protocole inchangé** (version `2`) : champs et genre optionnels. Migration `039` : table `service_updates`, `agents.service_rollback_version`, `alert_channels.attach_logs`.

### Ce que ça coûte

- **Les données ne sont pas restaurées** : une base migrée par la nouvelle version peut empêcher l'ancienne de démarrer ; le retour échoue alors et l'alerte passe en danger. La restauration d'une sauvegarde est un autre lot.
- **Un agent d'avant ce lot ne peut plus monter un service de version** (`deploy.rollback_unavailable`) : mettez-le à jour d'abord. Au 5 novembre, 6 agents sur 9 sont en v0.2.0.
- Sans HEALTHCHECK, « sain » veut dire « ne plante pas » : une erreur affichée sans plantage passe.
- Pendant la fenêtre (jusqu'à 5 min, 60 min au pire), la machine n'exécute pas ses autres ordres longs.
- Le journal joint peut contenir un secret que le masquage ne connaît pas, et il sort de la maison : éteignez la case du canal.
- Un client d'API qui crée ou modifie un canal sans envoyer `attach_logs` l'éteint.

## Ce qui n'est pas couvert (lot 2)

Les clés d'opérateur, la co-signature, le verrouillage de la clé du serveur, les plafonds de gestes, les secrets restic et la clé OPNsense hors du serveur. Un serveur compromis qui détient les mots de passe des dépôts lit toujours une sauvegarde restic qui passe par son relais. Un accès root à la machine de l'agent réécrit ses ancres.

## Le cache ZFS dans la mémoire (#328)

- **Avant** : utilisée = `MemTotal − MemAvailable`. Le noyau ne compte pas l'ARC ZFS dans `MemAvailable` : sur un Pi de 16 Go, 11 Go « utilisés » dont 9,9 Go (9,2 Gio) d'ARC, pour 29 Mo de plus gros processus.
- **Maintenant** : l'agent lit `size` et `c_min` dans `/proc/spl/kstat/zfs/arcstats` et retranche `size − c_min`. `mem_used` porte la valeur corrigée (les seuils d'alerte la lisent) ; `zfs_cache` porte l'ARC entier.
- **L'écran** : la tuile mémoire de la fiche dit « cache ZFS 9,2 Gio » à côté du pourcentage. Sans ZFS, ou avec un agent ancien : rien de plus, comme avant.
- **Le protocole de l'agent reste en version `2`** (champ optionnel). Un agent v0.6.0 ne corrige rien : sa mémoire reste fausse sur une machine en ZFS jusqu'à sa mise à jour.
- **Ce que ça coûte** : l'ARC est retranché en entier au-dessus de son plancher, même s'il ne se vide pas d'un coup ; sous forte pression la valeur peut être un peu basse. L'historique d'avant la mise à jour garde l'ancienne valeur ; la taille du cache n'est gardée que 48 h (le brut), pas dans les moyennes.
