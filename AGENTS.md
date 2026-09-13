# Travailler sur openCloud avec un agent

## Lire d'abord

1. [`docs/projet/12-methode.md`](docs/projet/12-methode.md) — branches,
   versions, CI, intégration des fonctionnalités.
2. [`docs/projet/17-conventions-code.md`](docs/projet/17-conventions-code.md)
   — conventions de code.
3. [`docs/projet/21-direction-artistique.md`](docs/projet/21-direction-artistique.md)
   — l'interface : couleurs, typographie, composants, ton, marque.
4. [`docs/projet/22-fonctionnement.md`](docs/projet/22-fonctionnement.md)
   — comment l'application tourne : acteurs, flux, ce qui est chiffré, ce qui
   est stocké. **Chaque fonctionnalité intégrée le met à jour.**

## Règles

- **Français** dans les documents et tout ce que voit l'opérateur ; **anglais**
  dans le code. Phrases courtes, tableaux, pas d'explication longue.
- **Vocabulaire interdit** : « nœud de contrôle » (dire *la machine openCloud*),
  « parc », « palier », « hors openCloud ».
- Chaque fait vit à un seul endroit ; les autres documents y renvoient.
- Le coût d'un choix se dit franchement, à côté de ce qu'il rapporte.
- Une décision se propose avec sa contrepartie ; **elle se prend par Lucas**.
- **Les fonctionnalités s'intègrent une par une, sur demande de Lucas** :
  analyser, décortiquer, adapter au besoin, puis intégrer proprement. Rien ne
  se reprend tel quel d'un autre projet.
- Ne jamais committer ni pousser sans demande. Pas de trailer d'IA.
- Commits courts, `type(portée): sujet` en français, sans corps ; **un commit
  par fonctionnalité** (`docs/projet/17-conventions-code.md`).
- **Jamais sur `dev` directement** : une branche par fonctionnalité
  (`feat/<nom>`, `fix/<nom>`), une pull request vers `dev`, CI verte, fusion
  par `merge`. `main` reçoit `dev` quand Lucas juge l'état stable ;
  `production` viendra plus tard pour les versions livrées
  (`docs/projet/12-methode.md`).
- **La version reste `v0.0.1`** tant que Lucas n'a pas décidé autrement.

## Sous-agents

Lancés avec **Sonnet** (mécanique : extraction, cartographie, vérification) ou
**Opus** (jugement : adaptation, synthèse, revue), jamais en héritant le modèle
de la session. Prompt précis : contexte, fichiers, format de sortie, style.
Si le résultat est mauvais, ré-itérer avec le même agent et un prompt plus ciblé/cadré.

## Code

Les conventions complètes sont dans
[`docs/projet/17-conventions-code.md`](docs/projet/17-conventions-code.md).
L'essentiel :

- **Lisible par un humain avant d'être court.** Une fonction fait une chose,
  se lit de haut en bas, tient à l'écran. Pas d'astuce, pas de one-liner
  malin, pas de générique inutile.
- **Le nommage fait le travail** : dossiers, fonctions et variables disent ce
  qu'ils sont. Verbe + objet pour une fonction ; jamais `m`, `tmp`, `data2`.
- **Identifiants en anglais, commentaires en français**, courts. Le pourquoi,
  jamais le quoi. Pas de commentaire par méthode par principe.
- Bibliothèque standard d'abord ; pas de réflexion, pas de cadre opaque.
- `gofmt`, `goimports`, `staticcheck` : la machine tient le style, l'humain
  garde l'attention pour le sens.
