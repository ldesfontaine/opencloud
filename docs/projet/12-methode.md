# Méthode de travail

1. **On développe en local.** Une fonctionnalité : ça marche ? On passe à la
   suite.
2. **On teste dans la CI GitHub**, basique, avec les bonnes pratiques :
   compilation, `go vet`, tests, `govulncheck`, `gosec`, scan des dépendances
   et des secrets, permissions minimales du workflow. Une VM seulement si un
   jour la CI ne suffit pas.
3. **Dépôt git vide, interface minimale**, une fonctionnalité à la fois.

## Les versions

Semver, et on reste longtemps en `0.x` : un projet à peine né n'est pas une
V1. Le **mineur** avance quand un jalon est livré (`v0.1.0` → `v0.2.0`), le
**correctif** pour les petites corrections entre deux (`v0.1.1`, `v0.1.2`).
Trois jalons prévus — `v0.1.0` exister, `v0.2.0` publier et certifier,
`v0.3.0` déployer, sauvegarder, observer. Rien au-delà tant que ça ne tourne
pas chez quelqu'un.

## Les outils de your-cloud

Regardés (`tools/`) : liés au laboratoire libvirt, à Tauri ou au registre
documentaire. Rien à reprendre tel quel.

## Table rase

La méthode de développement et de preuve de `your-cloud` n'est pas reprise
(`14-heritage-your-cloud.md`).
