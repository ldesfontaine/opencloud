# Travailler sur openCloud avec un agent

## Lire d'abord

1. [`docs/projet/README.md`](docs/projet/README.md) — l'index du cadrage.
2. [`docs/projet/11-decisions.md`](docs/projet/11-decisions.md) — le registre :
   acquis, à trancher, écarté. **Rien ne se décide ailleurs.**
3. Le document du sujet sur lequel on travaille, et lui seul.

## Règles

- **Français** dans les documents et tout ce que voit l'opérateur ; **anglais**
  dans le code. Phrases courtes, tableaux, pas d'explication longue.
- **Vocabulaire interdit** : « nœud de contrôle » (dire *la machine openCloud*),
  « parc », « palier », « hors openCloud ».
- Chaque fait vit à un seul endroit ; les autres documents y renvoient.
- Les questions ouvertes ne vivent **que** dans le registre, partie 2.
- Le coût d'un choix se dit franchement, à côté de ce qu'il rapporte.
- Une décision se propose avec sa contrepartie ; **elle se prend par Lucas**.
- Ne jamais committer ni pousser sans demande. Pas de trailer d'IA.
- Commits courts, `type(portée): sujet` en français, sans corps ; **un commit
  par fonctionnalité** (`docs/projet/17-conventions-code.md`).

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

