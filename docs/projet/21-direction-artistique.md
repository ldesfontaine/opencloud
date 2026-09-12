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
| Pastille d'état | hauteur 24, rayon 999, point 6 px |
| Barre latérale | 232 px, item 34 px de haut, rayon 8 |
| Icônes | trait 1,6 ; 16 px en ligne, 18 px dans le graphe |
| Ombres | aucune sur les cartes ; `0 1px 2px` sur boutons secondaires et items actifs ; portée sur menus et info-bulles |

## Composants

- **Barre latérale** : marque en haut, sections « Vue » et « Réglages »,
  compteurs en mono à droite (rouge quand il y a une alerte), en bas le
  commutateur clair/sombre puis le compte.
- **En-tête de page** : titre, sous-titre d'une phrase, actions à droite. Une
  seule action principale par écran.
- **En-tête de machine** : sélecteur de machine, pastille d'état, métadonnées
  séparées par des points (IP, système, version d'agent, dernier signal), puis
  les onglets : Résumé, Services, Domaines et certificats, Réseau, Sauvegardes,
  Journaux, Réglages.
- **Chiffre clé** : étiquette avec icône, valeur, jauge optionnelle, une ligne
  de contexte.
- **Tableau** : en-têtes en étiquette de section, lignes de 44 px minimum,
  valeurs en mono, menu « … » en fin de ligne.
- **Liste d'événements** : point d'état, fait en gras léger, contexte en
  dessous, moment à droite.
- **Graphe réseau** : fond pointé, nœuds de 184 px (icône, nom, sous-titre
  mono, point d'état), arêtes orthogonales arrondies, groupes en pointillé,
  nœud « Internet » en noir, sélection par anneau d'accent, inspecteur de
  300 px à droite.
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

## Pile front proposée

| Choix | Ce qu'il rapporte | Ce qu'il coûte |
| --- | --- | --- |
| Gabarits Go + HTMX + CSS à jetons (comme aujourd'hui) | Un seul binaire, pas de chaîne de build JS, CSP stricte | Le graphe réseau et le temps réel demandent un peu de JS à la main (SVG, SSE) |
| SvelteKit embarqué dans le binaire | Graphe et temps réel plus simples, composants réutilisables | Une chaîne de build, un second langage, une CSP à assouplir |

Tranché le 12 septembre 2026 : Go + HTMX, le graphe en SVG généré côté
serveur. À revoir seulement si le module `frontend-socle` impose autre chose.

## Tranché le 12 septembre 2026

1. Pile front : Go + HTMX + CSS à jetons.
2. Accent : indigo `#5b5be6` (sombre `#7c7cf0`).
3. Nom affiché : « openCloud ».
4. Signe de la marque : piste A, le nuage ouvert.
