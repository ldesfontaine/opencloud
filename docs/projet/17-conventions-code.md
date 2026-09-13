# Conventions de code

Ce qui n'est pas ici se décide dans la revue, puis s'écrit ici.

## Le dépôt

La disposition standard d'un programme Go (« Organizing a Go module »,
go.dev) : un point d'entrée sous `cmd/`, tout le reste sous `internal/`, que
le compilateur interdit d'importer d'ailleurs. Pas de `src/`, pas de `pkg/`.

```
cmd/opencloud/               # le seul main : il aiguille vers la commande
internal/<composant>/        # un package par composant
internal/server/             # la couche HTTP : API JSON, routes de l'agent et des pings, front servi
web/                         # le front React : src/, public/, dist/ embarqué par web/embed.go
migrations/                  # SQL numéroté, embarqué
docs/projet/                 # méthode, conventions, direction artistique
.github/                     # CI, gabarits de tickets
```

**Le Go vit dans `internal/`, le front dans `web/`.** `web/` et `migrations/`
sont à la racine parce qu'un `embed` ne remonte pas au-dessus de son
dossier, et qu'un humain les cherche là ; le seul Go de `web/` est
`embed.go`, qui expose `dist/` à `internal/server`. `web/dist` est produit
par `make front` : il n'est pas versionné, et tout build ou test Go le fait
d'abord. Quand `internal/server` grossira, il se découpera par consommateur
(`server/api`, `server/agentapi`, `server/ping`), pas avant.

**Un package naît avec sa fonctionnalité.** Pas de package vide qui promet.
Pas de package `util`. Un package = un concept.

Trois dossiers hors git, pour trois raisons :

| Dossier | Qui le crée | Contenu | `make clean` |
|---|---|---|---|
| `bin/` | `make build` | le binaire de dev, refait à chaque build, et l'outil plumber | jette le binaire, garde l'outil |
| `dist/` | `make release` | ce qu'une release publie : binaire versionné, `.deb`, `SHA256SUMS` | jette tout |
| `dev/` | `make run` la première fois, puis toi | `config.toml` et `state/` : ton `/etc` et ton `/var/lib` locaux | n'y touche jamais |
| `web/node_modules/`, `web/dist/` | `make front` | les paquets npm épinglés par le verrou, et le front compilé | vide `dist/`, garde `node_modules/` |

## La langue

**Identifiants en anglais** — packages, types, fonctions, variables, messages
de journal. **Commentaires en français**, courts. **Tout ce que voit
l'opérateur en français** — interface, refus, notifications, documentation.
Un fichier de chaînes par langue dès le départ, même s'il n'y en a qu'une.

## Lisible par un humain, avant d'être court

Le risque nommé : du code fonctionnel, dense, efficace — et qu'il faut relire
trois fois. Ce n'est pas ce qu'on veut.

- **Une fonction fait une chose**, se lit de haut en bas, tient à l'écran.
  Le chemin heureux à gauche : on sort tôt sur l'erreur, on n'imbrique pas.
- **Pas d'astuce.** Pas de one-liner malin, pas de générique là où un type
  concret suffit, pas d'idiome qui demande de « savoir ». Si une ligne
  impressionne, on la déplie.
- **La bibliothèque standard d'abord.** Pas de réflexion, pas de génération de
  code, pas de cadre opaque.
- **Un fichier, un sujet**, court : `queue.go`, `launch.go`, `follow.go`. Le
  type en haut, son constructeur, puis les méthodes dans l'ordre où on les
  appelle.
- **Ce qu'on refuse en revue** : une fonction à relire, une variable d'une
  lettre hors d'une boucle courte, une astuce, un commentaire qui répète le
  code.

## Le nommage fait le travail

Si les dossiers, les fonctions et les variables sont bien nommés, le code
s'explique seul et les commentaires deviennent rares.

| Quoi | Règle | Exemple |
|---|---|---|
| Dossier, package | un nom, un concept, au singulier | `runner`, `enroll`, `transport` |
| Fonction, méthode | verbe + objet ; dit ce qu'elle fait, pas comment | `DepositScript`, `FollowUnit`, `Enqueue` |
| Variable | ce qu'elle contient, pas son type | `machine`, `pending`, `scriptPath` — jamais `m`, `tmp`, `data2` |
| Booléen | une question | `isReachable`, `hasProxy` |
| Constante | nommée, jamais un nombre nu dans le code | `maxActionRuntime`, pas `1800` |
| Erreur | ce qui a été tenté | `ErrMachineUnreachable` |
| Abréviations | seulement les universelles | `id`, `ctx`, `err`, `cfg` — rien d'autre |

## Les commentaires

**Courts, simples, en français.** Ils disent le **pourquoi**, jamais le
**quoi** que le code dit déjà.

- Pas de commentaire par méthode ou par type par principe. Une fonction
  exportée n'a un commentaire que si son nom ne suffit pas.
- Un `doc.go` par package, **et le commentaire de package ne vit que là** —
  jamais en tête d'un autre fichier, même quand le package en a un principal.
  Trois lignes sur ce que le package fait et ne fait pas.
- On commente les **pièges et les décisions non évidentes** :
  `// fsync du dossier, sinon le rename peut se perdre à la coupure.`
- Un `// TODO(lucas): raison` porte toujours sa raison.

```go
// Dépose le script et ses paramètres, sans les lancer.
func (r *Runner) DepositScript(ctx context.Context, action Action) error {
	dir := r.actionDir(action.ID)
	if err := r.transport.Put(ctx, dir+"/run.sh", action.Script, 0o755); err != nil {
		return fmt.Errorf("deposit script: %w", err)
	}
	// params.env est lu par systemd, jamais par un shell : quoting fait par Go.
	return r.transport.Put(ctx, dir+"/params.env", action.ParamsEnv(), 0o600)
}
```

## Le build

Go ≥ 1.26. Les outils (`staticcheck`, `govulncheck`, `gosec`) sont
épinglés par version dans le Makefile et la CI — jamais `@latest`. `CGO_ENABLED=0`, `-trimpath`, version par `ldflags` depuis le tag.
SQLite `modernc.org/sqlite`. Un binaire, reproductible.

Le front demande Node ≥ 20.19 et npm. Les paquets sont épinglés à la version
exacte dans `web/package.json` (`save-exact`), le verrou est
versionné, la CI joue `npm ci`. Le npm 9 de Debian plante parfois pour
produire le premier verrou ; `npx npm@10 install` le fait, et `npm ci` le
rejoue ensuite sans souci. Sass est la version JavaScript pure : pas de
binaire par plateforme.

## Les composants

- Un composant expose un **type concret** ; celui qui le consomme déclare
  **chez lui l'interface** de ce qu'il en attend, et rien de plus
  (`web.Authenticator`, `auth.Store`). Un composant qui a plusieurs mises en
  œuvre (`transport` : local, ssh) définit la sienne.
- Les tests d'un consommateur passent par le vrai composant quand il est
  rapide et local (SQLite temporaire) ; un faux seulement pour ce qui est lent
  ou externe — SSH, DNS, GitHub.
- Un **constructeur** `New(deps…)` avec les dépendances explicites. **Aucune
  variable globale**, aucun `init()`.
- `context.Context` en premier argument de tout ce qui attend ou écrit.
- Les types du domaine portent les noms du modèle : `Machine`, `Environment`,
  `Service`, `Domain`, `Action`, `Batch`.

## Les erreurs

- `fmt.Errorf("verb object: %w", err)` — le verbe et l'objet, jamais le
  contexte complet ; il se lit dans la chaîne.
- Erreurs sentinelles par package : `var ErrNotFound = errors.New(…)`.
- **Un refus n'est pas une erreur** : type `Refusal{Cause, Remedy string}`,
  code de retour `2` des scripts, affiché tel quel en français. Une erreur,
  c'est ce qu'on n'avait pas prévu.

## La configuration

TOML, une struct typée, `pelletier/go-toml/v2`, validation par tags
(`go-playground/validator`). **Chargée à part, validée, puis publiée** —
jamais d'état à moitié appliqué. Clé inconnue = avertissement au démarrage.

## Les fichiers et l'état

- Répertoire d'état ouvert une fois par `os.Root` ; tout accès passe par lui.
- **Écriture atomique** : temporaire dans le même dossier, `fsync`, `rename`,
  `fsync` du dossier. Un seul helper, `internal/fsx`.
- SQLite en WAL. Une transaction par opération métier. Requêtes dans le
  package `store`, jamais ailleurs.

### Pourquoi des migrations

Le schéma de la base d'openCloud changera : une colonne ajoutée, une table
nouvelle. Une installation existante doit **suivre sans perdre ses données**.
Les migrations sont des fichiers SQL numérotés (`001_machines.sql`,
`002_actions.sql`…), embarqués dans le binaire, appliqués **dans l'ordre au
démarrage**, chacun noté dans une table `schema_migrations` pour ne jamais être
rejoué. Même principe que `php artisan migrate`, sans commande à lancer : le
binaire se met à niveau tout seul.

Rien à voir avec les migrations d'un **service** déployé (celles de son
application, jouées par `make update`) : ici il s'agit de la base d'openCloud
lui-même.

## L'exécution de commandes

- **Jamais de shell.** `exec.CommandContext(ctx, "/chemin/absolu", args…)`.
- **Environnement remplacé**, pas hérité : `PATH=/usr/sbin:/usr/bin:/sbin:/bin`,
  `LC_ALL=C`.
- **Délai maximum** systématique ; sortie bornée en taille.
- Tout appel dont un argument n'est pas une constante porte un commentaire qui
  dit **pourquoi il est borné** (`// bounded: validate.Domain`).
- Les valeurs de l'opérateur ne vont **jamais** dans un vecteur : `params.env`
  et fichiers rendus.

## Les scripts

```sh
#!/bin/bash
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C
# étape: … / résultat: … / exit 0 fait, 1 échoué, 2 refusé
```

Toutes les variables entre guillemets. `--` avant les positionnels. Ni `eval`,
ni `sh -c`, ni valeur dans un format `printf`. `shellcheck` propre. Un script
qui ne change rien le dit et sort `0`.

## Le web

Le serveur Go sert une API JSON sous `/api/` et le front compilé pour tout le
reste ; le navigateur fait le rendu. Les routes de l'agent (`/agent/`) et des
pings (`/ping/`) ne changent pas.

- Handlers **minces** : lire, valider, appeler un composant, répondre en
  JSON. Aucune logique métier. Le serveur rend des **faits** (instants ISO en
  UTC, durées en secondes), jamais du texte formaté : « vu il y a 12 s » se
  calcule dans le navigateur.
- Une route par lecture, une route par action : `GET /api/jobs/{id}`,
  `POST /api/jobs/{id}/actions/pause`, `DELETE /api/jobs/{id}`. Les chemins
  de l'API sont en anglais, comme le code ; les adresses des écrans restent en
  français (`/machines`, `/taches`), comme l'opérateur les lit.
- Une erreur de l'API est un seul mot : `{"error": "job.name_invalid"}`. Quand
  l'opérateur doit le lire, c'est une **clé du catalogue** et le front la
  traduit ; sinon un code court (`not_found`, `bad_json`, `internal`). Le front
  ne lit jamais un texte anglais du serveur.
- Toute écriture sous `/api/` passe la garde même-origine : `Sec-Fetch-Site`
  autre que `same-origin` ou `none` refusé, corps autre que
  `application/json` refusé. C'est ce qui remplace le jeton anti-CSRF des
  formulaires, et ce qui vaut tant qu'il n'y a pas d'authentification.
- CSP stricte inchangée : le front compilé n'a ni script ni style en ligne,
  un test le vérifie. Rien ne se charge depuis Internet.
- Le direct viendra en SSE, une seule connexion par onglet (fonctionnalité 4).

## Le front

`web/`, React + TypeScript strict + Vite, SCSS avec les jetons de la
direction artistique. Les mêmes règles qu'en Go, transposées :

- **Identifiants en anglais, commentaires en français**, courts, le pourquoi.
- Un composant par fichier, son `.scss` à côté, importé par lui. Les classes
  gardent les noms de la direction artistique (`card`, `pill`, `btn`).
- **Aucune chaîne visible en dur** : tout passe par `t("clé")` et les
  catalogues TOML servis par `/api/i18n/{code}` ; un test Go vérifie que
  chaque clé demandée existe dans les deux langues.
- Une page lit ses données par `useResource("/api/…")` et relit sur le signal
  de `useRefresh()` : c'est là que le direct se branchera.
- Pas de bibliothèque d'état ni de requêtes tant que `fetch` et quelques hooks
  suffisent. Toute dépendance nouvelle se décide en revue.
- `tsc --noEmit` et `vitest` en local et en CI (`make front-check`) ; la
  logique pure (durées, formats) a ses tests, les composants se vérifient
  dans le navigateur.

## Les journaux

`log/slog`, JSON vers journald. Un champ `action_id` sur tout ce qui touche une
action. Un marqueur interne pour distinguer les lignes d'openCloud de celles des
scripts relus.

## Les tests

- `_test.go` à côté du code. `go test ./...` sans réseau.
- **Le nom d'un test est une phrase** : `TestEnqueue_SameMachine_RunsInOrder` —
  il dit ce qu'il prouve.
- **`gofmt`, `goimports`, `staticcheck` obligatoires**, en local et en CI : la
  machine tient le style, l'humain garde l'attention pour le sens.
- **Fixtures figées** : la liste des actions et leurs schémas, l'arbre des
  routes, les réponses JSON de l'API (`internal/server/testdata/*.golden.json`,
  `go test ./internal/server -update` pour les réécrire après vérification). Tout
  changement casse un test — c'est voulu.
- Un composant se teste avec des faux des interfaces qu'il consomme.
- Les scripts : `shellcheck` + un test qui vérifie que chaque action du
  catalogue a son `run.sh` et inversement.
- **Idempotence testée.** Chaque `run.sh` est rejoué **deux fois** ; la seconde
  exécution doit sortir `inchangé` et code `0`. Vérifié en CI — c'est une
  propriété du produit, pas une bonne intention.

## La CI

`go build`, `go vet`, `staticcheck`, `tsc`, `vitest`, `vite build`,
`go test`, `govulncheck`, `gosec`, `shellcheck`, scan des secrets. `permissions: read` par défaut, délais,
`concurrency`. **Actions épinglées par SHA de commit**, et **Plumber** vérifie
la politique CI/CD (`.plumber.yaml`) à chaque exécution — bloquant, 100 points
exigés. `make ci` joue les mêmes commandes en local, `make plumber` la même
politique.

## Les commits

Convention *conventional commits*, courte :

```
feat(runner): reprise des actions préparées au démarrage
fix(enroll): le drop-in sshd est validé avant le reload
docs(cadrage): ports bruts rouverts au registre
ci: actions épinglées par SHA
```

- `type(portée): sujet` — types : `feat`, `fix`, `docs`, `ci`, `chore`,
  `refactor`, `test`. Sujet court, en français, à l'impératif, sans point.
- **Pas de corps**, pas de longue explication : les bons mots dans le sujet
  suffisent.
- **Un commit par fonctionnalité** — ni un par fichier, ni un par mot changé.
  Régulier, mais pas trop.
- Pas de trailer d'IA.
