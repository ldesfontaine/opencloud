# Données, sauvegardes et restauration

## Ce qui doit être sauvegardé

- les **données des services** ;
- les **bases de données** — une extraction cohérente, **pas** une copie de
  fichiers en cours d'écriture. **Le slot porte le nom réel de la base**, pas
  celui du service : deux bases d'un même moteur ne partagent alors aucun
  préfixe, donc la rétention de l'une n'efface jamais l'autre ;
- le **stockage ACME**, restauré **avant** Traefik pour ne pas réémettre
  (voir `06-reseau-et-certificats.md`) ;
- l'**état d'openCloud** — `/var/lib/opencloud/` sur la machine openCloud, par
  une **règle nommée** en plus de `/srv/data`. **Elle écrit sur une autre
  machine**, et l'interface dit **laquelle** et **quand** (`10-cycle-de-vie.md`) ;
- les **définitions de services** — peu volumineuses, et ce sont elles qui
  permettent de tout reconstruire.

## Deux niveaux

| | Où | Quand | Par qui |
|---|---|---|---|
| **Sauvegarde locale** | Sur la machine, sous `data/backups/` | Fréquente (quotidienne) | Une minuterie locale, indépendante de la machine openCloud |
| **Sauvegarde froide** | Hors de la machine : **un disque physique débranché**, transporté avec soi | Rare (tous les X mois) | Récupérée par `rsync` depuis un point central, puis **copiée à la main** |

**La récupération se fait en tirant, pas en poussant.** Une machine compromise
ne doit pas pouvoir effacer le dépôt central — si c'est elle qui envoie, elle
peut aussi tout écraser.

Le disque débranché a trois conséquences, et aucune n'est négociable :

- **le risque est assumé** — perte, vol ou casse ;
- **le disque est chiffré** ;
- **la copie est manuelle**, donc elle n'existe que si quelqu'un la branche —
  d'où l'obligation d'afficher **la date de la dernière copie froide** dans
  l'interface. Sans ça, on se croira protégé longtemps après avoir cessé de
  l'être.

> **Le point à regarder en face** : entre deux sauvegardes froides espacées de
> plusieurs mois, la seule copie qui existe est **sur la machine elle-même**. Si
> elle brûle, la perte va jusqu'à plusieurs mois de données. Deux façons de
> réduire l'écart sans changer le principe : rapprocher la sauvegarde froide, ou
> ajouter une **copie croisée entre machines de l'infrastructure**, plus
> fréquente et gratuite en stockage externe.

## Le chiffrement et la clé

- **Toute sauvegarde qui sort de l'infrastructure est chiffrée.** Ce n'est pas
  négociable : les sauvegardes contiennent les `.env` des services, donc des
  secrets en clair (voir `08-securite-et-secrets.md`).
- **La clé de chiffrement vit dans un gestionnaire de mots de passe** — hors de
  la machine, et openCloud ne la détient jamais. Corollaire à ne jamais
  enfreindre :
  **openCloud ne stocke pas cette clé**, même « temporairement ». Une machine
  volée ne doit pas contenir de quoi lire ses propres sauvegardes.

## L'élagage et la rétention

L'idée : élaguer en fonction de ce que le disque peut porter, plutôt que d'une
durée fixe qui ne veut rien dire d'une machine à l'autre. Elle a deux pièges,
et ils sont graves.

- **Un disque qui se remplit ne doit jamais entraîner la suppression de la
  dernière sauvegarde valide.** Il faut un **plancher** — un nombre minimum de
  sauvegardes conservées quoi qu'il arrive — qui prime sur le seuil d'espace.
- **On ne supprime jamais une sauvegarde tant que la suivante n'est pas
  vérifiée.** Sinon l'élagage détruit la seule copie utilisable au moment précis
  où la nouvelle a échoué.

Ce qui est retenu :

- **l'élagage laisse au moins 10 % d'espace libre** — **sur `/var` autant que
  sur `/srv`** : les journaux vivent dans `journald`, donc la saturation s'y
  déplace (`04-implantation.md`) ;
- **l'élagage libère réellement l'espace** : il ne se contente pas de retirer
  des références, et la place récupérée est mesurée après coup ;
- **une alerte se déclenche avant l'élagage** — son seuil est plus haut. Être
  prévenu que le disque monte, pas apprendre que la purge est partie ;
- **fréquence et rétention suivent l'espace disponible**, sous réserve du
  **plancher de sauvegardes** (`11-decisions.md`) ;
- une **rétention lisible** sert de base : N quotidiennes, N hebdomadaires, N
  mensuelles.

### Mesurer sans outil

Pas besoin de machinerie : `df` donne l'espace libre du système de fichiers,
`du` la taille d'un répertoire. On estime la taille de la prochaine sauvegarde
d'après les précédentes, on ajoute une marge d'erreur, et on compare au seuil
des 10 %. C'est suffisant — et surtout **c'est vérifiable à la main**, ce qui
compte davantage que la précision.

> **La décision se prend sur `df`, jamais sur `du`.** Un fichier supprimé mais
> tenu ouvert par un processus disparaît de `du` et occupe toujours le disque.
> Les deux chiffres divergent exactement quand ça compte.

### La notification

Une **notification système** est prévue dans quatre cas :

- quand l'**espace libre approche du seuil**, **avant** que l'élagage parte ;
- quand l'**élagage se déclenche** — c'est le signal que le disque est devenu
  trop petit, pas un détail de confort ;
- quand une **sauvegarde échoue**, ou qu'un **test de restauration échoue** ;
- quand une **tâche périodique cesse de se manifester** — sauvegarde,
  renouvellement, collecte.

> **Le quatrième cas est le plus important, et le moins naturel à écrire.** Une
> sauvegarde qui ne tourne plus n'écrit aucune ligne d'échec : c'est le
> **silence** qu'il faut surveiller (`09-observation-et-interface.md`).

La « copie froide trop ancienne » est un cas de ce quatrième, pas une règle à
part.

Locale d'abord — journal et interface —, branchée sur un canal de discussion
plus tard.

## Les principes

- **Une sauvegarde qui reste sur la machine ne protège que de l'erreur
  humaine.** D'où le niveau froid.
- **La seule preuve qu'une sauvegarde existe est une restauration réussie.**
  *Tester la restauration* est donc **une action du catalogue**, jouée
  **automatiquement après chaque sauvegarde** (`15-catalogue-actions.md`).
- Elle restaure dans un **dossier temporaire**, vérifie, puis supprime. Elle ne
  touche jamais aux données en place.
- **La clé de chiffrement se vérifie périodiquement.** openCloud rappelle
  l'échéance, l'opérateur fournit la clé, openCloud déchiffre une archive de
  test — et **ne la conserve pas**.
- **Restaurer est l'action la plus dangereuse du catalogue** : irréversible et
  interruptrice (voir `05-execution.md`).
