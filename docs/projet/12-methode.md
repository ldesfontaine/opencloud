# Méthode de travail

## Le cycle

1. **On développe en local**, une fonctionnalité à la fois. Ça marche ? On
   passe à la suite.
2. **La CI GitHub est la référence.** Ce qui passe en local n'est vert qu'une
   fois la CI verte.
3. **Les fonctionnalités existantes s'intègrent une par une.** Lucas donne un
   module venu d'un autre projet ; on l'analyse, on le découpe, on l'adapte au
   besoin d'openCloud, puis on l'intègre proprement. Rien ne se reprend tel
   quel.

## Les branches

| Branche | Rôle |
|---|---|
| `dev` | Le tronc. Rien ne s'y pousse directement. |
| `feat/<nom-court>`, `fix/…`, `docs/…`, `ci/…`, `chore/…` | Une branche par fonctionnalité, le même préfixe que le commit, en minuscules avec des tirets. |
| `main` | Reçoit `dev` quand Lucas juge l'état stable. Toujours installable. |
| `production` | Créée le jour où Lucas le décide, pour les versions livrées. |

- **Une pull request vers `dev`.** CI verte, relecture, puis **fusion par
  `merge`**, jamais par `squash` : les commits par fonctionnalité restent
  lisibles dans l'historique.
- **Rebase sur `dev` avant la PR** si la branche a vieilli, plutôt que des
  commits de fusion dans la branche.
- Deux travaux en parallèle ne se gênent pas : chacun sa branche, le conflit
  se règle dans la PR.
- Tant que le dépôt est privé sur un plan gratuit, GitHub n'applique pas ces
  règles : c'est une discipline.

## Les versions

**`v0.0.1`, et on y reste** tant que la direction artistique et toutes les
fonctionnalités voulues ne sont pas intégrées. Pas de `v0.1`, pas de `1.0`.
Le changement de version est une décision de Lucas.

## La CI

`.github/workflows/ci.yml` joue ce qui ne dépend pas du code : scan des secrets
et politique Plumber (`.plumber.yaml`). Les étapes Go — `gofmt`, `go vet`,
`staticcheck`, `go test`, `govulncheck`, `gosec`, build — reviennent avec le
premier code, épinglées par version. Les actions GitHub sont épinglées par SHA
de commit.
