# Périmètre — ce qu'openCloud est, et ce qu'il n'est pas

## L'intention

openCloud pilote **une infrastructure, une seule**. Il est installé *dans*
cette infrastructure, sur l'une de ses machines. Ce n'est pas un outil externe
qui se connecte à des infrastructures depuis l'extérieur.

Deux infrastructures à piloter, c'est deux installations indépendantes.

| Ce que ce choix coûte | Ce qu'il rapporte |
|---|---|
| Aucune vue commune à plusieurs infrastructures | Plus de plan de contrôle externe à héberger |
| Aucun inventaire global | Plus de question d'appartenance multiple |
| Autant d'interfaces que d'infrastructures | Plus de réplication d'état entre infrastructures |

C'est ce choix qui fait disparaître le problème le plus dur du cadrage.

## Les trois propriétés recherchées

- **S'attacher à n'importe quelle forme d'infrastructure**, y compris à des
  machines sans adresse publique.
- **Lisibilité** — ce que l'interface affiche est ce qui s'est produit sur la
  machine.
- **Simplicité assumée** — un mécanisme unique par besoin, quitte à couvrir
  moins de cas au départ.

## Ce qu'il fait d'abord, dans cet ordre

1. **Publier** — créer un hôte virtuel sur la bonne machine.
2. **Certifier** — obtenir et renouveler le certificat TLS du domaine.
3. **Sauvegarder** — poser une sauvegarde locale et la récupérer à froid.
4. **Observer** — santé des machines, puis trafic et journaux.

**Le déploiement d'applications vient après.** Le modèle du service est posé
(voir `03-modele.md`) pour que rien ne soit à défaire le moment venu, mais ce
n'est pas par là que l'outil commence.

## Ce qu'il n'est pas

- **Il n'est pas indispensable.** openCloud automatise ; tout ce qu'il fait
  doit pouvoir être fait à la main, au même endroit, de la même façon. Aucune
  machine ne dépend uniquement de lui pour fonctionner.
- **Il n'est pas propriétaire de la machine.** Ce qui a été fait à la main
  reste fait à la main. openCloud **n'écrase pas**, **ne réconcilie pas**. Le
  modèle est **impératif** : la machine fait foi.
- **Il ne fait pas de différence d'origine.** Tout ce qui suit la norme
  d'implantation (`04-implantation.md`) est un service — posé par openCloud ou
  à la main. Il reçoit les mêmes actions génériques : démarrer, arrêter,
  journaux, hôte virtuel.
- **Il n'adopte pas ce qui ne suit pas la norme.** Reprendre une machine rangée
  autrement était un objectif de `your-cloud` ; c'est abandonné — sans fin,
  chaque installation étant un cas particulier.

Le prix de ce choix, dit franchement :

- **Ce qu'affiche openCloud peut être en retard sur la réalité.** D'où
  l'obligation d'afficher **l'âge de l'information**, jamais un état présenté
  comme certain.
- **Pas de « remettre en conformité »** : si une machine dérive, openCloud
  signale, il ne répare pas seul.
- **La dérive se détecte, sinon ne pas réconcilier ne tient pas.**
  *Diagnostiquer* tourne **périodiquement**, son résultat est comparé au
  **dernier état connu**, et l'écart est **signalé** — jamais corrigé (`09`).

## Ce qui est écarté

- Pilotage de plusieurs infrastructures depuis une seule installation.
- Reprise et adoption de l'existant sur une machine.
- Réconciliation, remise en conformité, gestion de configuration désirée.
- Réplication d'état, consensus, bascule automatique.
- Répartition de charge, HAProxy, adresse IP flottante.
- Multi-utilisateurs cloisonnés, facturation, catalogue de services.

Le détail et le statut de chaque point : `11-decisions.md`.

## Les cinq principes

Hérités de `your-cloud`, gardés **en plus souple** : ce sont des intentions
fortes, pas des verrous.

1. **Rien ne s'applique en silence** : montré avant, constaté après, jamais
   paraphrasé. Une action courante et sans risque peut s'appliquer sans
   confirmation ; l'irréversible se confirme toujours.
2. **La machine fait foi** ; un plan parti n'est pas un plan appliqué.
3. **Un refus nomme sa cause et le geste qui la lève**, avant tout effet.
4. **Observer et agir sont deux chemins** ; compromettre l'un ne donne pas
   l'autre.
5. **Le pilotage n'est jamais dans le chemin des services** : éteindre openCloud
   n'éteint rien.

Et une règle de tenue : ce qu'openCloud pose vit sous `/srv` ; le reste du
système ne subit que des modifications **nommées, listées et réversibles**.

