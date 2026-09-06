# Méthode de travail

1. **On développe en local.** Une fonctionnalité : ça marche ? On passe à la
   suite.
2. **On teste dans la CI GitHub**, basique, avec les bonnes pratiques :
   compilation, `go vet`, tests, `govulncheck`, `gosec`, scan des dépendances
   et des secrets, permissions minimales du workflow. Une VM seulement si un
   jour la CI ne suffit pas.
3. **Dépôt git vide, interface minimale**, une fonctionnalité à la fois.

## Les branches

`main` est toujours verte et installable. Rien ne s'y pousse directement.

- **Une branche par ticket** : `feat/<nom-court>` pour une fonctionnalité,
  `fix/<nom-court>` pour un correctif, `docs/…`, `ci/…`, `chore/…` — le même
  préfixe que le commit. Le nom dit la chose, en minuscules, avec des tirets :
  `feat/enroll-ssh`, `fix/mot-de-passe-minimum`.
- **Une pull request vers `main`**, qui cite son ticket (`Closes #3`). La CI
  doit être verte ; on relit avant de fusionner.
- **Fusion par `merge`**, jamais par `squash` : les commits par fonctionnalité
  de la branche restent lisibles dans l'historique.
- **Rebase sur `main` avant la PR** si la branche a vieilli, plutôt que des
  commits de fusion dans la branche.
- Deux travaux en parallèle — un poste, une session dans le cloud — ne se
  gênent pas : chacun sa branche, le conflit se règle dans la PR, pas sur
  `main`.
- **Tant que le dépôt est privé sur un plan gratuit, GitHub n'applique pas
  cette règle** : pas de protection de branche, pas de ruleset. C'est une
  discipline — et une commande qui vérifie la CI avant de fusionner. La
  protection s'active le jour du plan Pro ou du passage en public.

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
