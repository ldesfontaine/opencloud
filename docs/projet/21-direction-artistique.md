# Direction artistique

> Statut : validée par Lucas le 12 septembre 2026, signe de la marque compris.
> Maquettes sur le canevas « Direction artistique openCloud »
> (six planches : vue d'ensemble, machine, réseau, thème sombre, système
> visuel, marque).

## Intention

Un tableau de bord d'hébergeur, récent, propre, technologique. Il montre l'état
de plusieurs machines sans jamais paraître chargé : peu d'éléments par écran, le
détail au clic ou en info-bulle.

| Référence | Ce qu'on en garde |
| --- | --- |
| openship | La coquille : barre latérale sobre, cartes blanches bordées, bouton principal noir, étiquettes de section en capitales |
| Railway | Le graphe : services en nœuds reliés, groupes en pointillé, inspecteur latéral |

## Couleurs

Une seule couleur d'accent. Le noir (ou le blanc en sombre) porte l'action
principale. Les cartes se distinguent par un trait, jamais par une ombre.

| Rôle | Variable | Clair | Sombre |
| --- | --- | --- | --- |
| Fond de page | `--bg` | `#f6f6f8` | `#0d0e12` |
| Surface (cartes, barre) | `--surface` | `#ffffff` | `#15171c` |
| Surface 2 (fonds discrets) | `--surface-2` | `#f1f2f5` | `#1c1f26` |
| Trait | `--line` | `#e5e7ec` | `#262a33` |
| Trait appuyé | `--line-strong` | `#d3d6de` | `#353a47` |
| Encre | `--ink` | `#101216` | `#eef0f4` |
| Encre 2 (texte secondaire) | `--ink-2` | `#4b5160` | `#b3b8c4` |
| Discret (métadonnées) | `--muted` | `#7a8194` | `#7d8494` |
| Accent | `--accent` | `#5b5be6` | `#7c7cf0` |
| Accent, lavis | `--accent-wash` | `#eeeefc` | `#1f2040` |
| OK | `--ok` / `--ok-wash` | `#1f9d57` / `#e4f6ec` | `#3ac776` / `#12301f` |
| Attention | `--warn` / `--warn-wash` | `#d78d0c` / `#fdf2dc` | `#f0a92a` / `#33260d` |
| Danger | `--danger` / `--danger-wash` | `#de4350` / `#fde9eb` | `#f06470` / `#3a1a1e` |
| Action principale | `--primary` / `--primary-ink` | `#101216` / `#ffffff` | `#eef0f4` / `#101216` |

Règles :

- L'accent sert aux jauges, aux liens, à la sélection et au focus. Pas aux
  boutons courants.
- Un état se lit par une pastille (fond lavis + texte + point), jamais par du
  texte coloré seul.
- **Inconnu** : pastille neutre (gris), jamais verte. Pour un état que le
  serveur ne confirme plus (la machine ne signale plus depuis 2 min) ; elle
  garde le dernier état connu dessous (« Dernier état : Actif, il y a 5 h »).
  Un état inconnu ne compte pas comme un problème et ne se dessine ni en
  vert ni en rouge.
- Le sombre reprend les mêmes rôles avec des valeurs propres ; on ne dérive pas
  le sombre du clair par inversion.

## Typographie

| Usage | Police | Taille / graisse |
| --- | --- | --- |
| Interface | Geist | 13 px / 400, interligne 1,45 |
| Titre de page | Geist | 22 px / 600, approche −0,02 em |
| Titre de carte | Geist | 14 px / 600 |
| Secondaire, dates | Geist | 12 px / 400, couleur discrète |
| Étiquette de section | Geist | 11 px / 500, capitales, approche 0,08 em |
| Chiffre clé | Geist | 26 px / 600, approche −0,03 em, unité en 14 px discrète |
| Identifiants, IP, images, ports, valeurs | Geist Mono | 12 px / 400 |

Les deux polices sont sous licence OFL et s'embarquent en `woff2` dans le
binaire, comme aujourd'hui. Rien ne se charge depuis Internet.

## Espaces, rayons, contrôles

| Élément | Valeur |
| --- | --- |
| Grille d'espacement | 4 · 8 · 12 · 16 · 20 · 24 · 32 |
| Marge de page | 28 px haut et bas, 32 px côtés |
| Carte | rayon 14, trait 1 px, padding 20 |
| Contrôle (bouton, champ, sélecteur) | hauteur 36, rayon 8 ; petit : 30, rayon 7 |
| Pastille d'état | hauteur 24, rayon 999, point 6 px ; petite (`small`) : 17, texte 11 px, point 5 px, pour un pied de tuile ou le titre d'une ligne de liste, sans les faire grandir |
| Barre latérale | 232 px, item 34 px de haut, rayon 8 |
| Sous 860 px | barre latérale en tiroir (280 px au plus, derrière un voile `--scrim`), en-tête de 56 px ; contrôle 44, petit 40 ; marge de page 20 / 16 |
| Icônes | trait 1,6 ; 16 px en ligne, 18 px dans le graphe |
| Ombres | aucune sur les cartes ; `0 1px 2px` sur boutons secondaires et items actifs ; portée sur menus et info-bulles |

## Composants

- **Barre latérale** : marque en haut, sections « Vue » et « Administration »,
  compteurs en mono à droite (rouge quand il y a une alerte), en bas le
  commutateur clair/sombre puis le compte.
- **En-tête de page** : titre, sous-titre d'une phrase, actions à droite. Une
  seule action principale par écran. La vue d'ensemble n'a pas de sous-titre
  tant qu'elle montre des machines : la tuile Machines dit déjà le compte.
- **Onglets** : un lien par onglet, le courant souligné. Quand la barre déborde
  (un téléphone), un fondu au bord cache ce qui reste hors de vue : à droite
  tant que la fin n'est pas atteinte, à gauche une fois défilée, par
  `mask-image` ; l'onglet courant est amené dans la vue au chargement. Rien ne
  change quand tout tient.
- **Menu déroulant** (`Select`, `EnumSelect`, `MachineSelect`) : jamais de
  `<select>` natif, dont la liste est dessinée par le navigateur et ne suit
  ni le thème ni cette page. Le déclencheur a la forme d'un contrôle (36 px,
  rayon 8, icône optionnelle, chevron à droite) ; dans un champ, il prend la
  largeur de la colonne et toute la boîte l'ouvre. Il montre toujours le
  libellé de la valeur reçue, l'invite discrète (« Choisir… ») quand aucune
  option ne la porte. La liste ouverte : surface, trait, rayon 10, ombre
  portée, option active en `--surface-2`, option choisie en 500 avec une
  coche d'accent ; sous le déclencheur, dessus s'il manque de place, jamais
  hors de la fenêtre ni coupée par une carte. Clavier : ↑ ↓ Début Fin,
  Entrée ou Espace choisit, Échap ferme et rend le focus, Tab ferme, une
  lettre saute à l'option qui commence par elle. **Au-delà de 8 options**,
  un champ de recherche en tête de liste, sans casse ni accent ; Début et Fin
  y restent au texte. Sous 860 px, 44 px pour le déclencheur et chaque
  option.
- **En-tête de machine** : sélecteur de machine, pastille d'état, métadonnées
  séparées par des points (IP, système, version d'agent, dernier signal), puis
  les onglets livrés : Résumé, Services, Disponibilité, Réseau,
  Journaux (un raccourci : un service par ligne, vers ses journaux) ; Sauvegardes
  et Réglages viendront avec leur écran. La fin de support du système, quand
  elle est activée, tient sur une ligne sous les métadonnées (pastille et
  phrase ; la phrase passe à la ligne sur un écran étroit, sans se couper) ;
  son détail est une carte sous l'historique.
- **Chiffre clé** : étiquette avec icône, valeur, jauge optionnelle, une ligne
  de contexte. Variante **compacte** (`Stat compact`, environ 60 px au lieu de
  125) : la valeur à 20 px, l'étiquette et le contexte l'un sous l'autre à sa
  droite, sans jauge. Pour une bande de chiffres en tête de page (la vue
  d'ensemble, les quatre chiffres d'une machine) ; une tuile qui porte une
  jauge garde la forme pleine. Avec `wideFoot`, le contexte passe sur toute la
  largeur sous le chiffre et l'étiquette (environ 66 px) : pour un contexte long.
  Une tuile compacte qui remplace une jauge garde sa couleur : le contexte passe
  en attention ou en danger aux mêmes seuils.
  Sur la vue d'ensemble, la tuile Alertes est la plus large et son contexte
  porte la plus urgente, tronquée, le texte entier en info-bulle. Le contexte
  d'une tuile qui dit un fait à traiter (« 2 à traiter », « 1 expire dans 5
  jours », « 1 à acquitter ») le dit en petite pastille au ton du fait ; ce
  qui va bien reste du texte neutre. Même règle pour l'échéance d'un
  certificat (pastille dès le seuil d'attention, texte discret avant) et le
  code de sortie d'une exécution (danger si non nul, neutre à 0).
- **Tableau** : pour ce qui est nombreux (conteneurs, tâches, exécutions,
  clés). En-têtes en étiquette de section, lignes de 44 px minimum, valeurs
  en mono. La ligne entière mène à la fiche (`RowLink` sur le nom : un vrai
  lien, Ctrl-clic compris ; survol en `--surface-2`) : plus de bouton
  « Ouvrir » en fin de ligne. Les actions secondaires vont dans un menu
  « … » juste après le nom, toujours visible, discret. Avec `fit`, chaque
  colonne prend la largeur de son contenu et le nom prend le reste : le vide
  ne s'étale plus entre les colonnes.
- **Carte d'objet** (`EntityCard` dans une `CardGrid`) : pour ce qui est peu
  nombreux et riche en état (machines, services déclarés, pages de statut).
  Icône, nom (14 px / 600), menu « … » à droite ; dessous le type et la
  pastille d'état ; puis deux à quatre faits (étiquette discrète, valeur en
  mono) et, au besoin, des mini-jauges. Toute la carte mène à la fiche (le
  lien du nom s'étire sur elle) ; survol par le trait appuyé, jamais
  d'ombre ; padding 16. La grille met autant de cartes de 260 px au moins
  que la largeur en tient, sans étirer deux cartes sur tout l'écran ; une
  seule colonne sur un téléphone. Là où cartes et tableau ont tous deux du
  sens, une bascule (`ViewToggle`, icônes grille et liste) les échange ; le
  choix est mémorisé par le navigateur, liste par liste. Une liste vide dit
  une phrase et son action principale sur une ligne (`EmptyLine`), pas une
  grande carte presque vide.
- **Liste d'événements** : point d'état, fait en gras léger, contexte en
  dessous, moment à droite.
- **Verdict** : en tête de la page d'un objet, la pastille d'état et le fait
  en une phrase (« 4 échecs de suite depuis 09:50 »), ce qui le précise
  (« Dernière réussite : 09:48, il y a 5 min »), puis les métadonnées. Un état
  qui demande l'opérateur teinte la carte de son lavis ; les autres la
  laissent blanche. Posé sur la page d'une tâche ; les autres pages suivront.
- **Frise** : un segment de 8 px par exécution, de la plus ancienne à la plus
  récente, aux couleurs d'état ; elle dit la tendance avant la liste.
- **Carte repliable** : ce qui se règle une fois (l'installation d'une tâche,
  les empreintes et le suivi d'une image) se replie sous son en-tête, chevron
  à droite, et passe sous l'état. Ouverte et en premier tant que la chose
  reste à installer ; repliée ensuite.
- **Page de réglages** (Paramètres) : des sections ancrées, toutes dans la
  page, une colonne de 880 px au plus avec sa navigation à côté (collante
  au-dessus de 1100 px, une rangée d'onglets-ancres sous 1100 px, qui défile).
  L'ensemble se centre : le vide d'un grand écran se partage. Ancres sans
  accent (`#acces`, `#fin-de-support`).
  - **`SectionHead`** : l'étiquette de section en capitales, une phrase qui dit
    ce qu'on y règle, à droite l'état de la section (pastille) quand elle en
    a un. Une section = un `SectionHead` puis une seule `Card`.
  - **`SettingRow`** : l'étiquette et sa description (une ligne, discrète) à
    gauche, le contrôle à droite ; empilée sous 860 px, contrôles de 44 px. Le
    contrôle a la taille de sa valeur (`width` : `short` 104 px pour un
    pourcentage ou un port, `medium` 280 px, `wide` 360 px). `block` : le
    contrôle passe sous le texte, pour une note ou un bloc à copier. Les
    lignes, blocs (`SettingBlock`) et formulaires d'une carte s'empilent,
    séparés par un trait.
  - **Pied de carte** (`SettingFoot`) : à gauche ce qui se dit en passant
    (« Enregistré », la portée du réglage), à droite l'**unique bouton
    principal** de la section ; « Enregistrer » y reste désactivé tant que la
    saisie n'a pas bougé par rapport à ce que le serveur a gardé. Ce qui
    détruit ou ferme (désactiver l'accès) va à part, sous un trait, dans une
    **zone de danger** (`DangerZone`), en bouton `danger` secondaire.
  - **`ConfirmInline`** : la confirmation d'une action d'une ligne. Le bouton se
    replie en « question [Confirmer] [Annuler] » à sa place, le focus sur
    « Annuler », Échap annule et rend le focus au bouton. Employée sous
    Paramètres (désactiver l'accès, révoquer un jeton), sur la fiche
    d'un pare-feu et dans le bloc Proxmox d'une machine (gestes sur une VM) ;
    les autres pages passent encore par `window.confirm`
    (`useAction({ confirm })`). La boîte `DeletePlanDialog` reste pour un
    plan de suppression, qu'il faut lire avant de confirmer.
- **Menu « … »** (`MoreMenu`) : les actions secondaires d'un objet, hors de la
  ligne. Une action impossible **reste au menu, grisée, avec sa raison**
  (`hint`, en dessous) : elle ne se cache jamais, et choisir une entrée grisée
  ne fait rien.
- **Formulaire** (`FormPage`) : l'en-tête de page et la carte dans une
  colonne de 560 px au plus, centrée comme Paramètres ; sur un téléphone,
  pleine largeur dans les marges de page. Toute page de formulaire
  (création, modification, jeton d'une machine) y passe. Un formulaire logé
  dans une carte partagée ou une grille (Paramètres, Sauvegardes, la mise à
  jour d'un incident) garde sa place ; ouvert sous la fiche d'un objet (la
  correction d'un incident), il en prend la largeur.
  - **Aide de champ** : une ligne, ce qu'il faut savoir pour remplir, pas
    plus. Le détail, s'il sert vraiment, va dans le « ? » à côté de
    l'étiquette (`tip` de `Field`) : une info-bulle qui passe à la ligne, au
    clavier comme à la souris ; Échap la referme.
- **Assistant** (`Wizard`) : la création d'un objet à plus de cinq champs
  environ ou à plusieurs notions (service, sonde, tâche, page de statut).
  Pas pour une page de réglages (Paramètres, Sauvegardes) ni pour un
  formulaire de trois champs : il ajoute des clics là où tout tient à
  l'écran. Colonne de 720 px (`FormPage width="wizard"`), la place de la
  barre d'étapes.
  - **Barre d'étapes** : une pastille numérotée et le nom ; la courante en
    encre pleine, une étape faite cochée, à venir en discret. Sous 860 px,
    une ligne, « Étape 2 sur 4 » : le nom suit, en tête de carte.
  - **Une étape à l'écran**, son titre en tête de carte (le focus y passe,
    il s'annonce). Précédent en secondaire, Suivant en principal, Annuler
    discret : une seule action principale. Entrée dans un champ vaut
    Suivant. Une étape en erreur retient : la note sous les champs dit
    pourquoi, et s'annonce. Le brouillon reste chez la page : rien ne se perd d'une étape
    à l'autre.
  - **Récapitulatif** en dernière étape : ce qui a été saisi, étape par
    étape, chacune avec « Modifier » ; puis l'action finale (« Déclarer »,
    « Créer »), la seule qui écrit. L'erreur du serveur s'y dit. Revenu d'un
    « Modifier », Suivant devient « Revenir au récapitulatif ».
  - **En modification**, l'assistant s'ouvre sur le récapitulatif et chaque
    étape de la barre y mène ; un saut en avant valide les étapes franchies.
- **Graphe réseau** : fond pointé, nœuds de 184 px (icône, nom, sous-titre
  mono, point d'état), arêtes orthogonales arrondies, groupes en pointillé,
  nœud « Internet » en noir, sélection par anneau d'accent, inspecteur de
  300 px à droite. Sur la vue d'ensemble, une version compacte sans zoom :
  nœuds de 148 px, un cadre en pointillé par machine, cadres empilés ou côte
  à côte selon la largeur (le rangement qui garde le plus grand texte dans
  240 px de haut), réduite pour tenir sur un écran de portable mais jamais
  sous 10 px de texte rendu.
- **Historique d'une machine** : quatre graphes d'aire de 120 px de haut (axes
  compris, texte de 11 px), sur une rangée au-dessus de 1280 px, deux en
  dessous, un sous 860 px ; l'axe du temps garde moins de repères quand le
  graphe est étroit. L'en-tête, les tuiles et l'historique tiennent sur un écran
  de portable (#141) ; l'agent et les ordres suivent dessous.
- **Info-bulle** : fond encre, texte 12 px, pour le détail qu'on ne veut pas à
  l'écran.

## Marque

- Mot-symbole « openCloud » : « open » en 500 encre 2, « Cloud » en 600 encre,
  approche −0,02 em, sans espace.
- Le signe se pose nu à côté du mot, dans la couleur du texte. Jamais de tuile
  noire derrière ; la tuile ne sert qu'au favicon.
- Le signe se dessine dans la grille 24 px et le trait des icônes.

| Piste | Idée | Coût |
| --- | --- | --- |
| **A · Nuage ouvert, retenue** | Un nuage d'un seul trait, laissé ouvert en bas : « open » | Discret ; le trait s'épaissit sous 16 px |
| B · Trois machines, écartée | Trois silhouettes qui font un nuage plein | Le plus lisible en favicon ; proche de signes existants |
| C · Nuage en réseau, écartée | Trois machines reliées, l'enveloppe fait le nuage | Dit le multi-machines ; ressemble à une icône de réseau |

Le signe retenu, grille 24 px, trait 2,2 arrondi :

```svg
<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
  <path d="M9 19a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9H14"/>
</svg>
```

## Ton

- Français, phrases courtes, verbe d'abord : « Déclarer un service »,
  « Sauvegarder », « Redémarrer ».
- Une alerte dit le fait, puis la cause, puis ce qu'on peut faire.
- Les nombres se lisent en mono, les unités en discret. Pas d'emoji.

## Pile front

| Choix | Ce qu'il rapporte | Ce qu'il coûte |
| --- | --- | --- |
| Gabarits Go + HTMX + CSS à jetons (12 → 13 septembre) | Un seul binaire, pas de chaîne de build JS, CSP stricte | Le graphe réseau et le temps réel demandent du JS à la main ; pas de composants, pas d'état côté navigateur |
| **Go + React embarqué (retenu le 13 septembre)** | Composants, état côté navigateur, graphe et direct plus simples ; le serveur Go et ses composants restent tels quels | Une chaîne de build (Vite, Node en CI), un second langage, une API JSON à dessiner par écran |
| SvelteKit embarqué | Comme React, écarté | Un cadre de moins connu |
| AdonisJS + Inertia | Un seul langage partout, écarté | Le serveur Go à réécrire, les modules Go de Projet_M plus réutilisables |

Tranché le 12 septembre 2026 : Go + HTMX. **Revu le 13 septembre 2026 :
Go + React embarqué**, SCSS avec les jetons de ce document, un fichier de
style par composant, briques minimales (react, react-dom, react-router,
TypeScript strict, Vite). Le graphe reste à dessiner en SVG, côté navigateur
désormais. Les jetons, la typographie et les composants de ce document ne
changent pas : ils sont portés tels quels.

## Tranché le 12 septembre 2026

1. Pile front : Go + HTMX + CSS à jetons ; **remplacée le 13 septembre par
   Go + React embarqué, SCSS à jetons** (voir « Pile front »).
2. Accent : indigo `#5b5be6` (sombre `#7c7cf0`).
3. Nom affiché : « openCloud ».
4. Signe de la marque : piste A, le nuage ouvert.
