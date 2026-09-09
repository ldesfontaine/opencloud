# Observation et interface

L'observation porte sur **l'infrastructure pilotée**, et sur elle seule : il
n'y a pas de vue au-dessus, puisqu'il n'y a pas de niveau au-dessus.

## Dans quel ordre

**Au départ — la santé de chaque machine** : joignabilité, processeur, mémoire,
disque, durée de fonctionnement, ancienneté de la dernière remontée. Une vue
d'ensemble, filtrable par machine et par environnement.

**Ensuite**, dans cet ordre :

1. **Par service ou conteneur** — état, redémarrages, consommation.
2. **Par site** — trafic vu par le proxy : requêtes, codes, latence, volume.
3. **Audience** — fréquentation réelle des sites (le terrain de Matomo). C'est
   une autre question que la santé des machines ; ne pas mélanger les deux dans
   la même vue.
4. **Alertes et supervision active** — Nagios, ou Icinga son successeur direct,
   devient pertinent quand il ne s'agit plus de regarder des courbes mais
   d'être prévenu : service tombé, certificat qui expire, disque qui se remplit.

## Les journaux

**Le WAF n'a pas besoin de journaux centralisés** : l'agent CrowdSec lit les
journaux Traefik **localement**, sur chaque machine, et ne remonte que ses
décisions à l'API centrale (`02-roles.md`).

La centralisation (Loki + Alloy) sert **le trafic par site** et l'affichage
dans l'interface. Elle est utile, pas fondatrice : elle peut venir après les
alertes.

## Les alertes

Quatre familles. La dernière est celle qui manque partout ailleurs.

| Alerte | Déclencheur |
|---|---|
| **Certificat** | Renouvellement en échec, échéance qui approche (`06-reseau-et-certificats.md`) |
| **Sauvegarde** | Sauvegarde échouée, **test de restauration échoué** (`07-donnees-et-sauvegardes.md`) |
| **Disque** | Espace libre qui approche du seuil, **avant** que l'élagage parte |
| **Absence de signal** | Une tâche périodique — sauvegarde, renouvellement, collecte — **cesse de se manifester** |

La dernière n'attend pas une erreur, elle attend un **silence** : une
sauvegarde qui ne tourne plus n'écrit aucune ligne d'échec.

**La dérive est signalée, jamais corrigée.** *Diagnostiquer* tourne
périodiquement, son résultat est comparé au dernier état connu, et l'écart
apparaît ici (`01-perimetre.md`).

## La pile

Critère : **la plus simple qui tienne sur une petite machine comme sur une
grosse.**

**Décidé** : **Beszel** pour les métriques — un hub en Go sur la machine
openCloud, un agent léger par machine, courbes processeur, mémoire, disque,
réseau et conteneurs sans configuration, alertes par courriel ou webhook — et
**Loki + Alloy** pour centraliser les journaux Traefik et ceux des services
(lus dans `journald`). Grafana en option plus tard. Netdata, plus détaillé
mais bien plus lourd, est écarté ; si Beszel déçoit à l'usage, on change
(tranché le 8 septembre 2026).

Le tout tourne **sur la machine openCloud**, **sous plancher de ressources
vérifié au préflight** — ordre de grandeur, 2 Go libres. En dessous : refus
d'installer Loki ou le collecteur, et **proposition d'une autre machine**
(`02-roles.md`, `15-catalogue-actions.md` §6).

## Ce que l'interface doit montrer

L'interface est **web** : pas d'application native. Sa forme (8 septembre
2026) : une **barre latérale à gauche**, repliable en rail d'icônes, qui porte
la navigation, l'interrupteur clair/sombre et le compte ; thème mémorisé par le
navigateur ; violet comme seul accent, Manrope, mono pour ce qui vient d'une
machine. **Moins de texte, plus de signes** : un refus dit sa cause puis le
geste qui la lève, une sortie de machine est interprétée en rapport, la sortie
brute repliée dessous, et un bouton dit ce qu'il fait avant qu'on clique.

| Vue | Ce qu'on y trouve |
|---|---|
| **Infrastructure** | Les machines, leur santé, les environnements |
| **Machine** | **Santé et actions ensemble** : statut, services hébergés, disque, modifications système appliquées, et les actions disponibles sur le même écran |
| **Service** | État, domaines, version déployée, journaux, sauvegardes, actions disponibles |
| **Domaines et certificats** | Quel nom pointe où, quel certificat expire quand, le **budget Let's Encrypt** (voir `06-reseau-et-certificats.md`) |
| **Bannissements** | Les adresses bannies par CrowdSec, avec le geste **débannir** (voir `08-securite-et-secrets.md`) |
| **Journal d'activité** | Qui a fait quoi, quand, avec quel résultat |

Deux règles posées ailleurs commandent ces vues : **rien ne s'applique en
silence**, et **l'âge de l'information est toujours affiché** — jamais un état
présenté comme certain (voir `01-perimetre.md`).

Trois exigences s'y ajoutent, et elles viennent du terrain
(`19-cas-d-usage.md`) :

- **Un seul écran qui montre et qui agit.** La vue Machine porte la santé et
  les actions ensemble. Beszel, Loki, CrowdSec restent des
  **détails** — jamais des interfaces obligatoires pour agir.
- **Le statut d'une machine est honnête** : joignable · SSH en échec · action
  en cours · dernière remontée datée. Jamais un « en ligne » ambigu
  (`02-roles.md`).
- **Avant d'exécuter**, l'écran d'une action montre ses quatre attributs, **le
  diff des fichiers** qui vont être posés et **le résultat de la validation à
  blanc** (`05-execution.md`).
