# Méthode de travail

1. **On développe en local.** Une fonctionnalité : ça marche ? On passe à la
   suite.
2. **On teste dans la CI GitHub**, basique, avec les bonnes pratiques :
   compilation, `go vet`, tests, `govulncheck`, `gosec`, scan des dépendances
   et des secrets, permissions minimales du workflow. Une VM seulement si un
   jour la CI ne suffit pas.
3. **Dépôt git vide, interface minimale**, une fonctionnalité à la fois.

## Les versions

Semver, et on reste longtemps en `0.x`. Les **`0.0.x`** sont les premières
briques, avant que l'outil serve à quelque chose : `v0.0.1` on se connecte,
`v0.0.2` il s'installe et se met à jour, `v0.0.3` une action en local,
`v0.0.4` une machine par SSH. **`v0.1.0` est la première base fonctionnelle**
— publier et certifier. Ensuite le **mineur** avance par jalon livré
(`v0.2.0` déployer, `v0.3.0` sauvegarder et observer) et le **correctif** pour
les petites corrections entre deux (`v0.1.1`, `v0.1.2`). Rien au-delà tant que
ça ne tourne pas chez quelqu'un.

## Les outils de your-cloud

Regardés (`tools/`) : liés au laboratoire libvirt, à Tauri ou au registre
documentaire. Rien à reprendre tel quel.

## Table rase

La méthode de développement et de preuve de `your-cloud` n'est pas reprise
(`14-heritage-your-cloud.md`).
