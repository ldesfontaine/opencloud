# Méthode de travail

1. **On développe en local.** Une fonctionnalité : ça marche ? On passe à la
   suite.
2. **On teste dans la CI GitHub**, basique, avec les bonnes pratiques :
   compilation, `go vet`, tests, `govulncheck`, `gosec`, scan des dépendances
   et des secrets, permissions minimales du workflow. Une VM seulement si un
   jour la CI ne suffit pas.
3. **Dépôt git vide, interface minimale**, une fonctionnalité à la fois.

## Les outils de your-cloud

Regardés (`tools/`) : liés au laboratoire libvirt, à Tauri ou au registre
documentaire. Rien à reprendre tel quel.

## Table rase

La méthode de développement et de preuve de `your-cloud` n'est pas reprise
(`14-heritage-your-cloud.md`).
