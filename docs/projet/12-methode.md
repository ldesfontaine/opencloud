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
| `main` | N'existe pas encore. Elle viendra recevoir `dev` quand Lucas jugera l'état stable, et sera alors toujours installable. |
| `production` | Créée le jour où Lucas le décide, pour les versions livrées. |

- **Une pull request vers `dev`.** CI verte, relecture, puis **fusion par
  `merge`**, jamais par `squash` : les commits par fonctionnalité restent
  lisibles dans l'historique.
- **Rebase sur `dev` avant la PR** si la branche a vieilli, plutôt que des
  commits de fusion dans la branche.
- Deux travaux en parallèle ne se gênent pas : chacun sa branche, le conflit
  se règle dans la PR.
- Le dépôt est public, mais aucune règle de protection n'est posée sur
  `dev` : GitHub n'applique rien, c'est une discipline.

## Les versions

**`v0.0.1`, et on y reste** tant que la direction artistique et toutes les
fonctionnalités voulues ne sont pas intégrées. Pas de `v0.1`, pas de `1.0`.
Le changement de version est une décision de Lucas.

## La CI

`.github/workflows/ci.yml` joue le scan des secrets, la politique Plumber
(`.plumber.yaml`), puis les cibles du `Makefile` : c'est lui qui liste les
étapes (`make ci`) et épingle les outils, avec `web/package.json` pour le
front. Le front se compile avant les tests Go, qui l'embarquent. Les
actions GitHub sont épinglées par SHA de commit.

`.github/dependabot.yml` propose chaque semaine, vers `dev`, une pull
request par écosystème (Go, npm, actions ; il met à jour le SHA et le
commentaire de version ensemble). `make front-check` lance d'abord
`npm audit --omit=dev --audit-level=high` : une dépendance de production
du front avec une faille haute ou critique arrête la CI (il faut le réseau).
Dans `go.mod`, `go 1.26` dit ce qu'il faut pour lire le module ; la ligne
`toolchain go1.26.9` fixe le correctif que la CI et les builds utilisent.

`.github/workflows/release.yml` publie les binaires Linux amd64 et arm64
(`make release`) dans la release `v0.0.1`, après les tests. **Lancement à
la main seulement** ; chaque lancement remplace la release précédente.

**Sobriété.** La CI tourne sur chaque pull request vers `dev` ou `main`, à
la poussée sur `main`, et à la main (`workflow_dispatch`) ; plus à la fusion
dans `dev`, que la PR vient de prouver. Un compte GitHub qui enchaîne un
nombre inhabituel d'exécutions automatiques peut être suspendu en attente de
vérification : un agent pousse une branche quand elle est prête (tests locaux
verts), regroupe ses corrections avant de repousser, et n'ouvre pas plus de
quelques PR à la fois.
