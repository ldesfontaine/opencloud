# Conventions de code

Propre comme SysWarden, sans sa lourdeur. Ce qui n'est pas ici se décide dans
la revue, puis s'écrit ici.

## Le dépôt

```
cmd/opencloud/main.go        # le seul main
internal/<composant>/        # un package par composant (16-architecture.md)
internal/scripts/            # run.sh, Makefile.common, gabarits — embarqués
web/templates/               # html/template
web/static/                  # HTMX, CSS
migrations/                  # SQL numéroté, embarqué
docs/projet/                 # ce cadrage
```

Pas de `pkg/`. Pas de package `util`. Un package = un concept.

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
- Un `doc.go` par package : trois lignes sur ce que le package fait et ne fait
  pas.
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

## Les composants

- Une **interface** par composant, définie dans son package, consommée par les
  autres. Les tests des autres composants utilisent un faux qui l'implémente.
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
  et fichiers rendus (`15-catalogue-actions.md`).

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

- Handlers **minces** : lire, valider, appeler un composant, rendre. Aucune
  logique métier.
- `html/template` seulement — l'échappement est automatique, on ne le
  contourne pas (`template.HTML` interdit sauf revue).
- Une route par action : `POST /machines/{id}/actions/{name}`. Pas de route
  « exécuter ».
- HTMX pour les fragments, SSE pour le direct. Pas de JavaScript maison au-delà
  de quelques lignes.
- Sessions côté serveur, cookie `Secure` `HttpOnly` `SameSite=Strict`, jeton
  anti-CSRF sur tout `POST`.

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
  routes, le rendu des gabarits. Tout changement casse un test — c'est voulu.
- Un composant se teste avec des faux des interfaces qu'il consomme.
- Les scripts : `shellcheck` + un test qui vérifie que chaque action du
  catalogue a son `run.sh` et inversement.
- **Idempotence testée.** Chaque `run.sh` est rejoué **deux fois** ; la seconde
  exécution doit sortir `inchangé` et code `0`. Vérifié en CI — c'est une
  propriété du produit, pas une bonne intention (`15-catalogue-actions.md` §1).

## La CI

`go build`, `go vet`, `staticcheck`, `go test`, `govulncheck`, `gosec`,
`shellcheck`, scan des secrets. `permissions: read` par défaut, délais,
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
