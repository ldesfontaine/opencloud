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
| `main` | Reçoit `dev` par pull request quand Lucas juge l'état stable ; toujours installable. Une version ne se tague que sur un commit de `main`, et seulement quand Lucas le décide (« Les versions »). |
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

La `v0.0.1` publiée à ce jour n'est pas signée : elle a été réécrite à la main
jusqu'ici. La vérification par signature commence à la première version
taguée par le workflow ci-dessous ; rien n'est tagué avant que Lucas décide.

### Le jour où Lucas décide d'une version

Une version est un **tag `vX.Y.Z` poussé sur un commit de `main`**. Elle
porte les binaires, `SHA256SUMS`, sa signature `SHA256SUMS.minisig` et une
attestation de provenance GitHub, et **ne se réécrit jamais**.

**1. Une fois : la paire de clés.**

| Étape | Geste |
| --- | --- |
| Tirer la paire | `opencloud release keygen -o release.key` sur son poste ; la commande écrit la clé privée (`0600`) et affiche la clé publique |
| Poser la clé privée | secret `RELEASE_SIGNING_KEY` du dépôt (Settings › Secrets and variables › Actions) = contenu de `release.key` ; une copie de `release.key` dans le coffre |
| Poser la clé publique | collée dans `internal/release/key.go` (`TrustedPublicKey`), par une pull request. Tant qu'elle est vide, le binaire refuse de vérifier et le job échoue : rien ne se publie sans clé |
| Régler le dépôt | ruleset de tags `v*` : création réservée aux administrateurs, ni mise à jour ni suppression ; Settings › General › « Releases immuables ». Ce sont des réglages du dépôt : le code ne peut pas les poser |

Perdre la clé privée, ou la changer, c'est ne plus reconnaître les versions
signées par l'ancienne : un binaire déjà installé garde la clé publique qu'il
embarque.

**2. À chaque version.** `dev` rejoint `main` par pull request, puis, sur le
commit de `main` choisi : `git tag -s vX.Y.Z <commit>` et
`git push origin vX.Y.Z`. Le workflow `release` fait le reste.

**3. Ce qui fait échouer le job** (aucune version n'est alors publiée) :

| Cause | Pourquoi |
| --- | --- |
| Le tag n'est pas sur un commit de `main` | une version vient de l'état jugé stable |
| La version existe déjà | une version ne se réécrit pas ; on en publie une autre |
| Le second build donne d'autres sommes que le premier | le binaire n'est pas reproductible |
| La clé privée est absente ou mal formée | rien à signer avec |
| La clé publique embarquée est vide, ou n'est pas celle de la clé privée | `release verify` refuse les deux binaires |

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
`toolchain go1.26.10` fixe le correctif que la CI et les builds utilisent.

`.github/workflows/release.yml` publie une version : les binaires Linux
amd64 et arm64 (`make release`), leurs sommes, la signature minisign des
sommes et l'attestation de provenance, après les tests. **Déclenché
seulement par un tag `vX.Y.Z` poussé sur un commit de `main`** ; une version
publiée n'est jamais réécrite (voir « Les versions »).

**Sobriété.** La CI tourne sur chaque pull request vers `dev` ou `main`, à
la poussée sur `main`, et à la main (`workflow_dispatch`) ; plus à la fusion
dans `dev`, que la PR vient de prouver. Un compte GitHub qui enchaîne un
nombre inhabituel d'exécutions automatiques peut être suspendu en attente de
vérification : un agent pousse une branche quand elle est prête (tests locaux
verts), regroupe ses corrections avant de repousser, et n'ouvre pas plus de
quelques PR à la fois.
