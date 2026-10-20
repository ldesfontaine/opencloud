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
| `bin/` | `make build` | le binaire de dev, refait à chaque build | jette le binaire |
| `dist-release/` | `make release` | les binaires `opencloud-linux-amd64` et `-arm64`, statiques, et `SHA256SUMS` ; c'est ce que `release.yml` publie. Pas de `.deb` | jette tout |
| `dev/` | `make run` la première fois, puis toi | `config.toml` et `state/` : ton `/etc` et ton `/var/lib` locaux | n'y touche jamais |
| `web/node_modules/`, `web/dist/` | `make front` | les paquets npm épinglés par le verrou, et le front compilé | vide `dist/`, garde `node_modules/` |

## La langue

**Identifiants en anglais** — packages, types, fonctions, variables, messages
de journal. **Commentaires en français**, courts. **Tout ce que voit
l'opérateur en français** — interface, refus, notifications, documentation.
Un fichier de chaînes par langue dès le départ, même s'il n'y en a qu'une.

**Le nom du produit ne s'écrit qu'à un seul endroit** : aucun littéral
« openCloud », « opencloud » ni préfixe de jeton en dehors de `internal/brand`
(`brand.ProductName`, `brand.Command`…). Les catalogues posent `{product}`,
`{command}` ou `{signature_header}`, les pages HTML `{{product}}`. Un test
(`TestNameOnlyInBrand`) l'impose ; les commentaires et les identifiants Go ne
comptent pas.

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
// trustedIdentity rend l'identité posée par un mandataire de confiance, ou
// rien si la requête ne vient pas de l'un d'eux ou ne la porte pas.
func (s *Server) trustedIdentity(r *http.Request, header string) string {
	if !s.trustsPeer(r) {
		return ""
	}
	// Plusieurs valeurs : le mandataire a ajouté la sienne au lieu
	// d'écraser, et la première peut venir du client.
	values := r.Header.Values(header)
	if len(values) != 1 {
		return ""
	}
	return strings.TrimSpace(values[0])
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
`002_heartbeats.sql`…), embarqués dans le binaire, appliqués **dans l'ordre au
démarrage**, chacun noté dans une table `schema_migrations` pour ne jamais être
rejoué. Même principe que `php artisan migrate`, sans commande à lancer : le
binaire se met à niveau tout seul.

Rien à voir avec les migrations d'un **service** déployé (celles de son
application, jouées par `make update`) : ici il s'agit de la base d'openCloud
lui-même.

## À venir : les actions

openCloud observe et n'agit sur aucune machine : aucun `exec.Command`,
aucun script, aucun catalogue d'actions dans le code. Les actions viendront
avec l'issue #125 (l'agent applique une mise à jour au clic). Les
conventions ci-dessous sont **écrites d'avance** pour ce jour-là ; rien ne
les vérifie encore, ni en local ni en CI.

### L'exécution de commandes

- **Jamais de shell.** `exec.CommandContext(ctx, "/chemin/absolu", args…)`.
- **Environnement remplacé**, pas hérité : `PATH=/usr/sbin:/usr/bin:/sbin:/bin`,
  `LC_ALL=C`.
- **Délai maximum** systématique ; sortie bornée en taille.
- Tout appel dont un argument n'est pas une constante porte un commentaire qui
  dit **pourquoi il est borné** (`// bounded: validate.Domain`).
- Les valeurs de l'opérateur ne vont **jamais** dans un vecteur : `params.env`
  et fichiers rendus.

### Les scripts

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
- Toute écriture sous `/api/` (POST, PUT, PATCH, DELETE) passe la garde
  même-origine : `Content-Type: application/json` exigé, **corps vide
  compris** (le front envoie `{}` pour une action sans corps), et un
  `Sec-Fetch-Site` présent autre que `same-origin` ou `none` refusé ; un
  `Sec-Fetch-Site` absent ne suffit pas à refuser. Refus : `403 bad_origin`.
  C'est ce qui remplace le jeton anti-CSRF des formulaires, et ce qui vaut
  tant qu'il n'y a pas d'authentification.
- Toute requête sous `/api/` et sur `/oauth/authorize` exige un `Host`
  reconnu : l'hôte de `public_url`, `localhost`, une IP littérale (v4 ou v6,
  avec ou sans port), l'hôte de `listen` ; derrière un mandataire de
  `trusted_proxies`, `X-Forwarded-Host` est jugé de même. Refus :
  `403 bad_host`, et une ligne de journal qui dit de régler `public_url`.
- CSP stricte inchangée : le front compilé n'a ni script ni style en ligne,
  un test le vérifie. Rien ne se charge depuis Internet.
- **Le direct** : `GET /api/events` en SSE, **une seule connexion par onglet**,
  ouverte par la coquille. L'événement ne porte qu'un **sujet** (`machines`,
  `jobs`) ; le front relit la ressource par l'API. Rien n'est rejoué : à la
  reconnexion le serveur dit `reconnected` et le front relit tout. Un
  composant qui change quelque chose de visible le publie sur
  `internal/live`, jamais depuis un handler.
- **La chaîne de middlewares**, de l'extérieur vers l'intérieur : panique →
  identifiant de requête → journal → limite de corps (1 Mio). Une enveloppe
  du `ResponseWriter` **relaie `Flush` et `Unwrap`**, sans quoi les deux flux
  SSE mourraient en silence ; c'est testé. `X-Request-ID` est tiré par le
  serveur, jamais lu du client.

## Le front

`web/`, React + TypeScript strict + Vite, SCSS avec les jetons de la
direction artistique. Les mêmes règles qu'en Go, transposées :

- **Identifiants en anglais, commentaires en français**, courts, le pourquoi.
- Un composant par fichier, son `.scss` à côté, importé par lui. Les classes
  gardent les noms de la direction artistique (`card`, `pill`, `btn`).
- **Aucune chaîne visible en dur** : tout passe par `t("clé")` et les
  catalogues TOML servis par `/api/i18n/{code}` ; un test Go vérifie que
  chaque clé demandée existe dans les deux langues.
  Les chaînes sont des formats Go (`%s`, `%d`), lus par le serveur et par le
  front : un pourcentage s'écrit `%%`, un test refuse un `%` seul.
- Une page lit ses données par les hooks de `api/<ressource>.ts`
  (`useProbes()`, `useProbe(id)`, bâtis sur `useResource`, relus sur le
  signal de `useRefresh()`) et écrit par ses fonctions `async`
  (`saveProbe`, `deleteProbe`…). Aucun chemin `/api/` hors de `api/`, sauf
  le flux du direct dans `useLive`.
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
- **Fixtures figées** : l'arbre des
  routes, les réponses JSON de l'API (`internal/server/testdata/*.golden.json`,
  `go test ./internal/server -update` pour les réécrire après vérification). Tout
  changement casse un test — c'est voulu.
- Un composant se teste avec des faux des interfaces qu'il consomme.
- **Avec les actions, à venir** : `shellcheck`, un test qui vérifie que
  chaque action du catalogue a son `run.sh` et inversement, et
  l'idempotence : chaque `run.sh` rejoué **deux fois**, la seconde exécution
  sort `inchangé` et code `0`. À poser en CI le jour où le premier script
  entre ; d'ici là, rien ne le vérifie.

## La CI

Les étapes sont celles de la cible `ci` du `Makefile`, qui fait foi ;
`.github/workflows/ci.yml` les appelle, précédées du scan des secrets et de
Plumber. Aucune liste n'est recopiée ici : elle divergerait. `permissions: read` par défaut, délais,
`concurrency`. **Actions épinglées par SHA de commit**, et **Plumber** vérifie
la politique CI/CD (`.plumber.yaml`) à chaque exécution — bloquant, 100 points
exigés. `make ci` joue les mêmes commandes en local ; `make plumber`, pour
la même politique en local, est **à venir** : la cible n'existe pas encore,
Plumber ne tourne qu'en CI.

## Les commits

Convention *conventional commits*, courte :

```
feat(runner): reprise des actions préparées au démarrage
fix(enroll): le drop-in sshd est validé avant le reload
docs(cadrage): ports bruts rouverts au registre
ci: actions épinglées par SHA
```

- `type(portée): sujet` — types : `feat`, `fix`, `docs`, `ci`, `chore`,
  `refactor`, `test`. Sujet court, en français, sans point : il **décrit**
  ce que le commit change, comme les exemples ci-dessus. Pas de sujet qui
  enchaîne plusieurs changements : c'est le signe de deux commits.
- **Pas de corps**, pas de longue explication : les bons mots dans le sujet
  suffisent.
- **Un commit par fonctionnalité** — ni un par fichier, ni un par mot changé.
  Régulier, mais pas trop.
- Pas de trailer d'IA.
