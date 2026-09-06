# L'exécution — files, actions, tâches périodiques

## Une seule voie d'exécution

- **Les actions passent par SSH**, depuis la machine openCloud, avec un
  utilisateur dédié dont le `sudo` n'autorise **qu'un lanceur root-owned**,
  `oc-launch <id>`, qui revalide l'identifiant et lance `systemd-run` lui-même
  (`15-catalogue-actions.md` §1). C'est un **filet** contre l'erreur et les
  escapes, pas un rempart contre une clé volée — et c'est dit tel quel.
- **Une action est un script shell versionné** dans openCloud, jamais une
  commande libre composée à la volée. **Ansible n'entre que si le catalogue
  révèle trop de répétition** — et alors il remplace, il ne s'ajoute pas.
- **La machine openCloud sur elle-même** : même script, **même transport** —
  SSH vers `localhost`, comme n'importe quelle machine. L'unité systemd
  d'openCloud est fermée (`NoNewPrivileges`, aucune capacité) : le service ne
  peut pas faire `sudo` sur sa propre machine, et on ne la rouvre pas pour ça.
  Un seul transport, un seul chemin à éprouver. La machine openCloud s'enrôle
  par `sudo opencloud enroll-local`, joué une fois sur elle (tranché le
  6 septembre 2026, ticket #20).
- **Les remontées passent par un collecteur standard**, pas par du code maison
  (voir `02-roles.md`).

## Deux mécanismes à ne pas confondre

| | Où | Déclenché par |
|---|---|---|
| **Les files d'actions** | Tenues par la machine openCloud | Un humain depuis l'interface, ou l'ordonnanceur |
| **Les tâches périodiques locales** | Sur chaque machine | Elles-mêmes, sans rien demander à personne |

## Une file par machine

**Il n'y a pas de file globale.** Chaque machine a **sa** file et son
**exécuteur**, qui prend les actions **une par une**, dans l'ordre, et rien de
plus.

Ce que ce choix règle d'un coup :

- deux actions ne peuvent jamais se gêner sur une même machine — c'est
  structurel, il n'y a aucun verrou à écrire ;
- les machines avancent **en parallèle** sans avoir à se coordonner ;
- l'état est lisible sans explication : « machine A, 3 actions en attente ».

Le mécanisme sous-jacent est banal et disponible partout : une table, un
processus qui la lit, un service qui le maintient en vie. Une table SQLite ou
PostgreSQL lue par un service systemd suffit, sans aucune dépendance.
**Le langage n'est pas le sujet.**

### Les actions qui concernent tout le monde

« Sauvegarder toutes les bases » ne rentre pas dans une file de machine — elle
les concerne toutes. **Une action de portée infrastructure se décompose au
moment du dépôt**, en une action par machine concernée, déposée dans chaque
file.

L'interface garde le lien entre elles : un **lot** (« sauvegarde générale du
5 septembre ») et les N actions qu'il contient, chacune avec son propre
résultat. Une machine peut échouer sans faire échouer les autres, et on ne
rejoue que celle-là.

**Le lot affiche, par machine, trois états et pas deux :**

| État | Ce qu'il dit |
|---|---|
| `réussi` | L'action a été appliquée sur cette machine |
| `échoué` | Elle est partie et n'a pas abouti — le constat est là |
| `non tenté` | Elle n'est jamais partie : injoignable, ou le lot s'est arrêté avant |

Le troisième est le plus utile : il dit exactement **ce qu'il reste à rejouer**
(`16-architecture.md`).

## L'action ne vit pas dans la connexion SSH

C'est le point important. Si openCloud lançait la commande et la gardait dans
la connexion, **la connexion deviendrait le point fragile** : une coupure
réseau, un redémarrage de la machine openCloud, et le `sshd` distant envoie un
`SIGHUP` au groupe de processus. Résultat le plus probable : **l'action meurt à
mi-parcours**. Le cas restant n'est pas meilleur — elle continue, mais plus
personne ne voit ni sa sortie ni son code de retour.

**Donc l'action ne s'exécute jamais *dans* la connexion.** SSH ne sert qu'à
trois choses, brèves toutes les trois :

1. **déposer** le script de l'action sur la machine,
2. **la démarrer détachée**,
3. **revenir plus tard** lire son état et sa sortie.

Sur une machine où systemd est déjà requis, l'outil est tout trouvé :

```
systemd-run --unit=oc-action-<id> --property=RuntimeMaxSec=1800 <script>
```

Ce que cette seule ligne apporte :

- **le processus appartient à systemd**, plus du tout à la connexion ;
- **`systemctl show oc-action-<id>`** rend l'état *et le code de retour*, même
  des heures après ;
- **la sortie est dans `journald`** — ce qui règle du même coup la sortie au fil
  de l'eau et la reprise du suivi ;
- **le délai maximum est appliqué par la machine elle-même**
  (`RuntimeMaxSec`) : une action emballée est tuée sur place, même si openCloud
  est éteint — il n'y a plus de délai à faire respecter à distance.

`screen`, `tmux`, `nohup` ou `setsid` restent des replis valables si systemd
venait à manquer, au prix d'avoir à écrire soi-même le code de retour et le
journal dans un fichier.

> **Ce que ça change** : au redémarrage, l'exécuteur **retrouve** ses actions.
> Il connaît leurs identifiants, demande à chaque machine ce que sont devenues
> les unités correspondantes, et reprend le fil. Une coupure ne perd plus rien.

Le script, sa sortie et son code de retour restent aussi sur la machine, sous
`/var/lib/opencloud/actions/<id>/` — ce qui rend le geste « aller voir »
possible **sans** openCloud.

## Le journal de transaction

Lu dans le code de SysWarden (`annexes/lecture-syswarden.md`), et c'est ce qui
manquait à la file. **Chaque action est journalisée avant d'être lancée** :

1. `préparée` — le script, son empreinte, l'unité `oc-action-<id>` attendue,
   le retour arrière s'il existe ;
2. `en cours` — l'unité tourne ;
3. `appliquée` ou `échouée` — après relecture de journald et du code de retour.

Au démarrage d'openCloud, ou après une coupure, **toute ligne `préparée` ou
`en cours` déclenche une reprise** : relire l'unité et journald sur la machine,
puis conclure. La reprise est déterministe. Rien ne reste dans le flou.

## Comment sait-on qu'une action est bloquée

L'exécution étant détachée, « état inconnu » cesse d'être la réponse par
défaut.

| Ce qu'on observe | Ce que ça veut dire |
|---|---|
| L'unité n'existe plus et `journald` a son résultat | Terminée, réussie ou échouée, avec son code de retour. Rien d'incertain. |
| L'unité tourne encore mais n'écrit plus depuis N minutes | **Le vrai signal de blocage** |
| `RuntimeMaxSec` a expiré | La machine a tué l'action elle-même. Filet de sécurité, pas premier signal : une mise à jour peut légitimement durer vingt minutes. |
| La machine est injoignable | On ne sait rien *pour l'instant* — ce n'est pas « perdue » : l'information reviendra avec la machine. |

Le deuxième cas suppose que **toute action écrive au fil de l'eau**, une ligne
par étape. Sans ce flux, il n'y a rien à observer.

## Ce que l'opérateur peut faire d'une action bloquée

**Annuler dans la file n'annule pas ce qui tourne sur la machine.** La commande
continue là-bas. D'où trois gestes distincts, nommés honnêtement :

| Geste | Ce qu'il fait réellement |
|---|---|
| **Abandonner le suivi** | La file passe à autre chose ; la machine fait ce qu'elle fait |
| **Relancer** | Seulement si l'action est rejouable sans dégât |
| **Aller voir** | L'interface donne la commande exacte à taper en SSH |

**Jamais un « annuler »** qui laisserait croire que la machine s'est arrêtée.

## Les quatre attributs d'une action

L'interface doit les montrer **avant** d'exécuter.

| Attribut | Question | Valeurs |
|---|---|---|
| **Portée** | Qu'est-ce qu'elle touche ? | infrastructure · machine · environnement · service · domaine |
| **Lieu** | Où s'exécute-t-elle ? | sur la machine cible · sur la machine openCloud · chez un tiers (DNS) |
| **Réversibilité** | Peut-on revenir en arrière ? | réversible · irréversible |
| **Interruption** | Coupe-t-elle un service en marche ? | oui · non |

> Les deux derniers commandent l'interface : **une action irréversible ou
> interruptrice se confirme, les autres non.** C'est ce qui évite à la fois le
> clic dangereux et la confirmation qui lasse.

### Ce que l'écran montre en plus des quatre attributs

Quatre étiquettes ne disent pas ce qui va être écrit. L'écran montre donc
aussi, avant d'exécuter :

- **le diff des fichiers qui vont être posés** — ligne à ligne, contre ce qui
  est déjà sur la machine ;
- **le résultat de la validation à blanc** — `make config`, `sshd -t`,
  `visudo -c` selon l'action.

Une action qui ne change rien le dit **avant** de partir
(`15-catalogue-actions.md` §1).

## Premier catalogue

> Le catalogue complet, avec paramètres, refus et séquences, est dans
> `15-catalogue-actions.md`. Ce tableau reste comme vue d'ensemble.

À compléter avant de coder. Il dira aussi si les scripts suffisent ou si Ansible devient nécessaire.

| Action | Portée | Lieu | Irréversible | Coupe |
|---|---|---|---|---|
| Enrôler une machine | machine | machine openCloud → machine | non | non |
| Poser les répertoires de base | machine | machine | non | non |
| Ouvrir un port brut | — | — | **hors périmètre** | — |
| Installer le proxy (Traefik) | machine | machine | non | non |
| Installer collecteur / WAF | machine | machine | non | non |
| **Créer un hôte virtuel** | domaine | machine | non | non |
| Supprimer un hôte virtuel | domaine | machine | non | **oui** |
| **Demander un certificat** | domaine | machine + DNS | non | non |
| Renouveler un certificat | domaine | machine + DNS | non | non |
| **Créer l'enregistrement DNS** | domaine | machine openCloud + DNS | non | non |
| Déployer un service | service | machine | non | brièvement |
| Démarrer / arrêter un service | service | machine | non | **oui** (arrêt) |
| Configurer la sauvegarde | service ou machine | machine | non | non |
| Lancer une sauvegarde | service ou machine | machine | non | selon la méthode |
| Tester la restauration | service | machine | non | non |
| **Restaurer une sauvegarde** | service | machine | **oui** | **oui** |
| Récupérer les sauvegardes froides | infrastructure | machine openCloud | non | non |
| Mettre à jour le système | machine | machine | non | possible |
| **Redémarrer la machine** | machine | machine | non | **oui** |
| Supprimer un service | service | machine | **oui** (le service, pas les données) | **oui** |

Ce que ce tableau montre déjà :

- **Presque tout s'exécute sur la machine cible.** La machine openCloud ne fait
  qu'enrôler, écrire dans le DNS, faire tourner un jeton et récupérer les
  sauvegardes froides. Elle déclenche, elle n'exécute pas.
- **Trois actions seulement sont vraiment dangereuses** — restaurer, supprimer,
  redémarrer. Ce sont celles qui méritent une confirmation montrant ce qui va
  se passer.
- **Les actions se ressemblent beaucoup** : poser un fichier, recharger un
  service, vérifier le résultat. C'est précisément le genre de répétition
  qu'Ansible sait éviter — mais il faut voir le catalogue complet avant de
  conclure.

## Les tâches périodiques sur les machines

**Préférer les minuteries systemd au cron.** À usage égal, elles apportent
exactement ce dont openCloud a besoin pour en rendre compte : les journaux dans
`journald`, un état interrogeable (`systemctl list-timers`), la gestion des
exécutions qui se chevauchent, et le rattrapage d'une exécution manquée pendant
une extinction. **Un cron, lui, échoue silencieusement** — et l'interface n'a
alors rien à afficher.

À poser sur chaque machine : sauvegarde et élagage, renouvellement ACME,
remontée de métriques.

## Faut-il un agent, finalement ?

La question revient, et elle mérite d'être posée avec des faits plutôt qu'avec
une intuition. **Ce qu'un agent achèterait réellement**, ligne par ligne :

| Ce qu'on pourrait vouloir | Un agent est-il nécessaire ? |
|---|---|
| Ranger les fichiers selon la norme | **Non.** Un répertoire ne dérive pas ; la norme n'a besoin d'aucun processus pour rester vraie (`04-implantation.md`) |
| Sauvegarder, élaguer, renouveler les certificats sans openCloud | **Non.** Les minuteries systemd le font déjà, et mieux : état interrogeable, journaux, rattrapage |
| Remonter des métriques, des journaux, se défendre | **Non.** Ce sont des collecteurs et CrowdSec, des services déployés comme les autres (`02-roles.md`) |
| Exécuter une action qui survit à la coupure | **Non.** `systemd-run` détache déjà l'exécution de la connexion |
| **Recevoir des ordres sans être joignable de l'extérieur** | **Oui** — c'est le seul cas réel. Aujourd'hui traité par un lien monté depuis la machine (`06-reseau-et-certificats.md`) |
| **Décrire la machine sans qu'on la lui demande** | **Peut-être.** Un agent pousse son état ; sans lui, openCloud doit interroger, et ce qu'il affiche a toujours un peu de retard |

Autrement dit : **le seul besoin qu'un agent servirait mieux est celui d'une
machine injoignable de l'extérieur**, et il existe déjà une réponse. Tout le
reste est couvert par des outils standard que personne n'a à écrire ni à mettre
à jour.

Ce qui reste vraiment ouvert n'est donc pas « agent ou pas », mais **par quel
outil passent les actions**. Tranché : des scripts par SSH, Ansible seulement si
le catalogue révèle trop de répétition.

## Les deux parcours à écrire

Ils traversent tout le projet et serviront de premier test grandeur nature :

1. **Créer un hôte virtuel** — choisir la machine et l'environnement, donner le
   domaine, désigner la cible (un conteneur ou un chemin), openCloud écrit la
   configuration et recharge le proxy.
2. **Obtenir un certificat** — pour ce domaine, en DNS-01 via Cloudflare, puis
   le voir apparaître dans la page certificats avec sa date d'expiration.

Ils mettent en jeu la file d'actions, le contrat de service, le routage, le
DNS, la sauvegarde du stockage ACME et l'affichage. Les écrire de bout en bout
révélera ce que le cadrage a laissé dans l'ombre.
