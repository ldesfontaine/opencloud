# Ligne directrice des modules

> Statut : **proposition** du 31/10/2026, à valider par Lucas avant la reprise
> des écrans. Elle complète la [direction artistique](21-direction-artistique.md)
> (jetons, typographie, composants), qui ne change pas : ici, ce qu'un module
> doit être, de la page à l'agent.

## Le principe

Un module répond toujours aux mêmes quatre questions, dans le même ordre et au
même endroit de l'écran :

1. **Est-ce que ça va ?** (le verdict, en tête)
2. **Qu'est-ce qu'il y a ?** (la vue principale)
3. **Qu'est-ce que je peux faire sur cette chose-là ?** (l'inspecteur, puis la fiche)
4. **Qui a fait quoi ?** (le journal du module)

Qui a appris un module sait lire les autres.

## L'anatomie d'une page de module

| Zone | Ce qu'elle porte | Règle |
| --- | --- | --- |
| En-tête | titre, une phrase qui dit à quoi sert le module, actions à droite | **une seule action principale** ; c'est la création de l'objet du module |
| Verdict | pastille + le fait en une phrase + ce qui le précise ; 2 à 4 chiffres à droite | teinté de son lavis seulement si l'opérateur doit agir ; sinon blanc. Jamais de chiffre décoratif : chaque chiffre est un fait qu'on peut cliquer |
| Onglets | la vue principale d'abord, puis les objets de configuration, puis l'historique | 2 à 4 onglets ; « Historique » toujours en dernier |
| Vue principale | ce qu'on surveille : matrice, cartes ou tableau selon la densité | ce qui demande l'opérateur **en tête** (non couvert, en échec, inconnu) |
| Inspecteur | à droite, 340 px, sans quitter la page ; s'ouvre au clic sur une ligne ou une case | verdict de l'objet, frise, faits clés, actions ; « Ouvrir la fiche » pour le détail |
| Fiche | la page d'un objet, avec ses onglets | mêmes zones que la page de module, à l'échelle de l'objet |

## Les actions

- **Visibles, jamais cachées.** Une action impossible reste grisée, avec sa
  raison en une ligne (règle du `MoreMenu`, étendue aux boutons d'inspecteur).
- **Ce qu'un agent peut faire porte la puce `MCP`.** C'est la parité promise :
  tout geste marqué existe comme verbe d'API, avec le même nom, la même portée
  et le même refus. Un geste sans verbe n'a pas la puce.
- **Ce qui détruit ou réduit va dans la zone de danger**, marqué
  « Interface », jamais exposé comme verbe : supprimer, réduire une rétention,
  révoquer, retirer. Confirmation par la saisie du nom de l'objet quand la
  perte est irréversible (`ConfirmInline` sinon). Le journal garde l'auteur.
- **Restaurer ou remplacer en place** est toujours une action d'interface ;
  « à côté » (dans un dossier neuf) peut être un verbe.
- **Chaque geste dit son retour arrière** avant d'agir, dans l'inspecteur
  (« Retour : … ») ; un geste sans retour le dit aussi.

## Les états

Les quatre tons de la direction artistique, et rien d'autre : réussi, à
surveiller, en échec, inconnu. Deux états en plus, propres à openCloud :

| État | Dessin | Sens |
| --- | --- | --- |
| **Observé** | point creux | openCloud le voit mais ne le pilote pas (une tâche vzdump de l'hôte, un service lancé à la main) |
| **À venir** | point gris, texte « à venir » | déclaré, pas encore en service (une cible VPS prévue) |

Un état « observé » n'est ni vert ni rouge : il dit seulement qui tient la
chose.

## Les composants partagés à créer

| Composant | Premier module | Réutilisé par |
| --- | --- | --- |
| `Matrix` (lignes groupées, colonnes, cases cliquables, légende) | Sauvegardes | Clés SSH (remplace la matrice actuelle), droits des agents, pare-feu (zones × zones) |
| `Inspector` (panneau latéral, verdict, frise, faits, actions, zone de danger) | Sauvegardes | Machines, Services, Pare-feu, Tâches |
| `FileExplorer` (instantanés à gauche, fil d'Ariane, liste, sélection, sorties) | Sauvegardes | données d'un service, fichiers d'un conteneur, journaux archivés |
| `PolicySentence` (une règle lue comme une phrase, chaque terme modifiable) | Sauvegardes | alertes, tâches planifiées, rétention des journaux |

`FileExplorer` reçoit une source d'arborescence (une fonction qui liste un
dossier) et une liste de sorties autorisées ; il ne sait rien des sauvegardes.
Clavier : ↑ ↓ pour bouger, Entrée pour ouvrir, Espace pour cocher, Retour
arrière pour remonter, Échap pour fermer.

## Ordre de reprise proposé

1. **Sauvegardes** : maquette `maquettes/sauvegardes.html`, écrans avec les
   chantiers #372 (restauration test) et #373 (matrice et explorateur). C'est
   là que naissent `Matrix`, `Inspector` et `FileExplorer`.
2. **Machines** : verdict et inspecteur, la page la plus vue.
3. **Services** : l'inspecteur pour déployer, mettre à jour, sauvegarder ;
   c'est la page du socle v2.
4. **Pare-feu** : la matrice zones × zones, lecture d'abord.
5. Les autres (tâches, sondes, alertes, statut) suivent la grille sans
   chantier propre : à chaque fois qu'on y touche.

Chaque reprise : la maquette d'abord, validée, puis le code ; la page
`VisualSystem` montre chaque composant nouveau avant son premier usage.
