# Conventions de code

Ce qui n'est pas ici se décide dans la revue, puis s'écrit ici.

## Le dépôt

La disposition standard d'un programme Go (« Organizing a Go module »,
go.dev) : un point d'entrée sous `cmd/`, tout le reste sous `internal/`, que
le compilateur interdit d'importer d'ailleurs. Pas de `src/`, pas de `pkg/`.

```
cmd/opencloud/               # le seul main : il aiguille vers la commande
internal/<composant>/        # un package par composant
internal/reader/             # l'interface Reader : les lectures de la base sans règle, partagées (types seulement)
internal/agentwire/          # le protocole agent ↔ serveur : en-têtes, corps JSON, sections du signal (types seulement)
internal/server/             # la couche HTTP : montage, middlewares, API JSON, MCP, front servi
internal/server/agentapi/    # les routes /agent/… : enrôlement, défi, flux, signal, journaux
internal/server/ping/        # les routes /ping/… d'un cron
internal/server/public/      # la page de statut publique sous /statut : page, instantané, chaînes, direct
internal/server/sse/         # la boucle des flux text/event-stream, partagée par l'administration et la page publique
internal/server/httpx/       # les gestes de réponse que la racine et ses sous-paquets partagent : JSON, événement SSE, 429, adresse du pair
internal/server/servertest/  # ce que les tests de server et de ses sous-paquets partagent
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
d'abord. `internal/server` se découpe par consommateur : l'agent
(`server/agentapi`), les pings (`server/ping`) et la page de statut publique
(`server/public`) en sont sortis, chacun avec ses seules dépendances, son
format de réponse et ses limiteurs ; la racine garde le montage des routes, la
chaîne de middlewares, les en-têtes (la politique de cadre de `/statut` se
décide par le chemin) et l'adresse du client, qu'elle leur passe en fonction.
Un sous-paquet n'importe jamais `server` ; ce qu'il partage avec la racine
(écrire du JSON ou un événement SSE, répondre 429, lire l'adresse du pair)
vit dans `server/httpx`, une seule fois.

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
- **Un fichier, un sujet**, court : `probe/check.go`, `probe/plan.go`,
  `status/incidents.go`. Le
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
  (`probe.Store`, `status.Settings`). Un composant qui a plusieurs mises en
  œuvre définit la sienne.
- **Aucune variable globale** hors des erreurs sentinelles, aucun `init()`.
- `context.Context` en premier argument de tout ce qui attend ou écrit.
- Les types du domaine portent les noms du modèle (`24-modele-cible.md`) :
  `Machine`, `Container`, `Probe`, `Service` et `Unit` de l'annuaire.

### Les couches

| Couche | Paquets | Ce qu'elle fait | Jamais |
| --- | --- | --- | --- |
| Entrée | `cmd/opencloud` | Aiguille vers la commande, ouvre la base, construit les composants et les branche | Une règle métier |
| HTTP | `internal/server` (racine) | Le montage des routes, la chaîne de middlewares, les gardes (hôte, même-origine, opérateur), l'API JSON, MCP, le front servi | Du SQL, une règle métier |
| HTTP par consommateur | `server/agentapi`, `server/ping`, `server/public`, `server/sse` | Les routes d'un seul consommateur, avec ses dépendances, son format de réponse et ses limiteurs (« Le dépôt ») | Importer `server` |
| HTTP partagé | `server/httpx` | Les gestes de réponse de la racine et des sous-paquets : `WriteJSON`, `WriteEvent`, `WriteTooMany`, `PeerHost`, `ConvertAll`, `TimeOrNil` | Une route, un composant |
| Composants | `machine`, `container`, `probe`, `heartbeat`, `resource`, `directory`, `manifest`, `status`, `alert`, `update`, `lifecycle`, `settings`, `auth`, `mcp` | Les règles : vérifier, valider, écrire, publier sur le direct, donner les faits aux alertes | HTTP, SQL, relayer une lecture de la base sans rien y ajouter |
| Lecture partagée | `internal/reader` | L'interface `Reader` : les lectures de la base sans règle (`List*`, `Get*`, `Count*`) que le serveur, la page de statut et le fichier de l'annuaire partagent ; `*store.DB` la satisfait (`var _ reader.Reader = (*DB)(nil)`) | Une requête, une règle, une écriture ; rendre les types d'un paquet qui l'importe (`status`, `manifest`) |
| Base | `internal/store` | Tout le SQL : requêtes, transactions, migrations, les comptes (`CountContainers`, `CountContainerUpdates`), et les contrôles qui doivent tenir dans la transaction de l'écriture (unicité de l'annuaire) | Une règle qu'un composant peut porter |
| Protocole agent | `internal/agentwire` | Les types, en-têtes et sections du signal, figés par les agents installés | Une règle, un appel |
| Outils | `refusal`, `live`, `fsx`, `seal`, `egress`, `ratelimit`, `ident`, `wait`, `brand`, `lang`, `mail`, `registry`, `dockerapi`, `sampler`, `hostinfo`, `trust`, `proxysecret`, `config` | Un concept chacun, sans règle d'openCloud. `ratelimit` : un limiteur par clé (`New`) ou par source (`BySource`, le /64 calculé dedans), balayé au passage ; `ident.New` : les identifiants de seize hexadécimaux (`machine.NewID`, un UUID, reste à part) ; `wait.For` : une pause qui s'arrête à l'annulation | Importer un composant |
| Agent | `agent`, `dockerwatch` | Ce qui tourne sur une machine gérée | Importer `server` ou `store` |

Qui peut importer qui :

| Paquet | Importe | N'importe jamais |
| --- | --- | --- |
| `cmd/opencloud` | tout | — |
| `server` | ses sous-paquets, les composants, `reader`, les outils, `web` | `store` |
| `server/agentapi`, `server/ping`, `server/public`, `server/sse` | les composants qu'il sert, les outils, `agentwire`, `server/httpx` ; `public` importe `sse` | `server`, `store`, un autre sous-paquet |
| `server/httpx` | la bibliothèque standard | `server`, un autre sous-paquet, un composant, `store` |
| Un composant | d'autres composants (types et gestes), les outils ; `status` et `manifest` importent `reader` | `server`, `store`, `cmd` |
| `reader` | les composants dont il rend les types : `machine`, `container`, `probe`, `heartbeat`, `directory`, `alert`, `settings` | `server`, `store`, un composant qui l'importe (`status`, `manifest`) : Go refuserait le cycle |
| `store` | les composants, pour leurs types ; `reader`, pour l'assertion ; `seal` ; `migrations` | `server` |
| `agentwire` | les types qu'il transporte : `container`, `probe`, `update`, `sampler` | un composant dont il ne transporte pas les types (`machine`), `server`, `store` |
| Un outil | d'autres outils, la bibliothèque standard | un composant, `server`, `store` |
| `agent`, `dockerwatch` | les composants (leurs types), les outils, `agentwire` | `server`, `store` |
| `servertest`, `storetest`, `smtptest` | ce qu'un banc demande, `store` compris | — ; seuls des `_test.go` les importent |

Deux composants qui ont besoin l'un de l'autre ne s'importent pas tous les
deux (Go le refuse) : l'un déclare une interface et reçoit l'autre par un
`Set*` au câblage (`containers.SetUnits(declared)`,
`declared.SetRematcher(containers)`).

### Lire et écrire un autre domaine

| Geste | Par où | Exemple |
| --- | --- | --- |
| Lire pour dériver : un état, un compte, un écart | La base, par le `reader.Reader` reçu au câblage ; une lecture propre à sa règle, que lui seul fait, reste dans son `Store` | `status` lit `ListProbes`, `ListContainers`, `LoadSettings` par son `reader` ; `probe.Store` a `DeclaredServiceExists`, `update.Store` a `ListContainers` |
| Lire ce qu'un composant montre sans rien y ajouter | Le `Reader`, directement : le composant ne relaie pas | Le serveur et MCP lisent `ListProbes`, `GetHeartbeat`, `CountAlerts` par `s.reader` |
| Lire ce qui porte une règle | Le composant | `machine.List` (en ligne ou non), `container.Tracker.List` (les vivants), `probe.Days` (la fenêtre), `alert.Engine.List` (la borne) |
| Écrire | **Toujours le composant du domaine**, par une interface déclarée chez celui qui écrit | `status` écrit le titre de la page par `settings.SetStatusPage` ; `manifest` applique par les gestes de `directory`, `probe` et `heartbeat` |

Le composant porte les règles d'une écriture : existence, validation,
plafond, direct, alertes. Une écriture par la base les sauterait ; une
lecture ne change rien, elle prend le chemin court. Un composant qui lit
par le `Reader` ne peut pas y rendre ses propres types (`status`,
`manifest`) : ses listes passent par lui (`status.Incidents`,
`manifest.Applies`), et c'est le seul relais qu'on garde ; `container.Tracker.Get`
reste pour l'annuaire, qui lit une fiche archivée comprise et ne peut pas
recevoir le `Reader` sans cycle.

### Le patron d'un composant

Le modèle est `probe.Service` (`internal/probe/service.go`). Un composant
nouveau suit le même ordre, de haut en bas du fichier :

| Pièce | Règle | Dans `probe` |
| --- | --- | --- |
| Interfaces | Déclarées chez lui, ce qu'il attend et rien de plus : les écritures et les lectures de sa règle dans `Store` ; ce qu'il lit d'un autre domaine par le `reader.Reader` | `Store`, `Commander`, `Assignable`, `Watcher`, `Alerter` ; `status.New(store, reader, …)` |
| Constructeur | `New(store, logger)` ; les collaborateurs par `Set*`, au câblage, avant le démarrage | `SetWatcher`, `SetAlerter`, `SetCommander`, `SetLocalRunner`, `SetClock` |
| Définition | Fonction pure, testable sans base : `Definition.Complete()` pose les valeurs par défaut, puis `Validate()` rend le premier refus | `internal/probe/probe.go` ; `heartbeat` n'a que `Validate()`, faute de valeur par défaut |
| Geste | Vérifie que ce qu'il référence existe, valide, écrit, journalise, appelle `changed()`, relit et rend l'objet | `Create`, `Update`, `Pause`, `Delete` |
| `changed()` | Prévient le `Watcher` ; jamais depuis un handler | `ProbesChanged(machineID)` |
| Lectures | Seules celles qui ajoutent une règle : une fenêtre, un filtre, une vérification, une conversion. Une liste, un objet ou un compte tels qu'en base se lisent par le `Reader` ; le composant ne les relaie pas | `Days`, `Uptimes`, `Certificates` ; pas de `List` ni de `Get` |
| Boucle | `Watch(ctx)` : rollup, purge, relances, jusqu'à l'annulation du contexte | `Watch` |

- **Nommage** : `Watcher` est le direct (`internal/live`), `Alerter` le
  moteur des alertes ; `nil` est toléré pour les deux. `Listener` ne
  s'emploie plus.
- **L'existence se vérifie dans le composant**, avant toute écriture : la
  machine et le service d'une sonde, la machine d'une tâche quand elle en a
  une. Sans cela la clé étrangère refuse en erreur brute, et MCP ou
  `opencloud apply`, qui appellent le composant sans handler, passeraient
  sans contrôle. Ordre des refus, celui que l'API a toujours rendu : l'objet
  modifié (404), la machine, la définition, puis le service.
- **Compter puis insérer** (un plafond) se tient sous un verrou du composant
  (`probe`, `status`, `alert`) ou dans la transaction du store (annuaire).
  Les deux marchent ; on ne les unifie pas.

### Les réglages

- Toute écriture d'un réglage passe par `settings.Service`
  (`SetLanguage`, `SetMailRelay`, `SetDiskThresholds`,
  `SetLifecycleEnabled`, `SetStatusPage`). Il porte la règle de chaque clé
  (port par défaut du relais, mot de passe laissé vide gardé) et relit,
  modifie, réécrit dans une transaction.
- Un composant qui lit un réglage au moment de servir reçoit une fonction au
  câblage : `CurrentLanguage`, `CurrentMailRelay`, `CurrentDiskThresholds`,
  `CurrentLifecycleEnabled`. Elle relit la base à chaque appel et rend une
  valeur de repli en échec.
- Un handler appelle `settings.Service`, jamais le store.

### Le store

| Règle | Comment |
| --- | --- |
| Seul lieu du SQL | Aucune requête hors de `internal/store` ; `*store.DB` satisfait l'interface `Store` de chaque composant |
| Une transaction par geste | `db.inTx(ctx, "what", func(tx *sql.Tx) error {…})` : `begin what` et `commit what` dans les erreurs, annulée si la fonction échoue |
| Une ligne, un `scanX` | `scanAlert(row scanner)` lit une ligne ; une liste est `collect(rows, scanAlert)`, jamais `nil` |
| Une ligne attendue | `notFoundIfNoRow(result, alert.ErrNotFound)` : aucune ligne touchée rend la sentinelle du domaine |
| Unicité | Une contrainte violée devient la sentinelle du domaine (`isUniqueViolation` → `machine.ErrNameTaken`) ; l'annuaire vérifie avant d'écrire, dans la même transaction |
| Les parties d'une liste | Une requête pour toute la liste, regroupée en Go par identifiant (`attachServiceParts`, `attachMembers`, `attachIncidentParts`), jamais une requête par ligne |
| Compter | En SQL (`COUNT`, `SUM(CASE…)`), jamais une liste comptée en Go ; une règle écrite en Go et en SQL (`container.NeedsAttention`, `containerNeedsAttention`) a son test de parité |

### Ce qu'on ne fait pas

| Idée | Pourquoi non |
| --- | --- |
| Un handler CRUD générique | Chaque ressource a sa règle (plafond, relecture, liste des liens) : le générique se troue au deuxième cas |
| `Repository[T]` dans `store` | Les requêtes diffèrent toutes ; un dépôt générique cache le SQL qu'on veut lire |
| `decode[T]` à la place de `readAPI` | Gagne une ligne par handler, ne retire aucune logique |
| Une conversion domaine → JSON par réflexion | Réflexion interdite ; la forme JSON est figée par les goldens |
| Un `Publish(topic)` générique à la place des `XChanged` | Perd le contrôle du compilateur sur qui publie quoi |

Un générique se justifie quand la même forme revient souvent et que seul le
type change : `httpx.ConvertAll`, `orEmpty`, `collect`, `sliceOf`, `guarded`.
Petit, nommé, à cette échelle.

## Les erreurs

- `fmt.Errorf("verb object: %w", err)` — le verbe et l'objet, jamais le
  contexte complet ; il se lit dans la chaîne.
- Erreurs sentinelles par package : `var ErrNotFound = errors.New(…)`.
- **Un refus n'est pas une erreur** : la demande est impossible en l'état,
  l'opérateur lit sa clé en français. Une erreur, c'est ce qu'on n'avait pas
  prévu : le journal la garde, l'opérateur lit `internal`.
- **Toute règle métier refuse par `refusal.*`** : le domaine déclare sa clé
  avec l'erreur, `refusal.Invalid` ou `refusal.Conflict`
  (`internal/refusal`). Elle se traduit une seule fois : le serveur par
  `s.failed`, MCP par `s.toolFailed`, le fichier de l'annuaire par
  `refusal.KeyOf`. Jamais d'`errors.Is` qui choisit une clé dans un handler.
- `s.apiRefuse` n'est que pour ce que le handler juge lui-même : un
  paramètre de requête, un en-tête, une limite du serveur. Les quelques refus
  de domaine dont le statut n'est ni 422 ni 409 (404 à clé propre, 403, 503)
  sont listés dans `22-fonctionnement.md`, « Les refus ».

## La configuration

TOML, une struct typée, `pelletier/go-toml/v2`, validation par tags
(`go-playground/validator`). **Chargée à part, validée, puis publiée** —
jamais d'état à moitié appliqué. Clé inconnue = avertissement au démarrage.

## Les fichiers et l'état

- Répertoire d'état ouvert une fois par `os.Root` ; tout accès passe par lui.
- **Écriture atomique** : temporaire dans le même dossier, `fsync`, `rename`,
  `fsync` du dossier. Un seul helper, `internal/fsx`.
- SQLite en WAL. Les requêtes et les transactions : « Le store », plus haut.

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

- Handlers **minces**, toujours la même forme : lire (`readAPI`, sinon
  `400 bad_json`), appeler le composant, répondre à son erreur par
  `if s.failed(w, r, err, probe.ErrNotFound) { return }`, écrire
  (`writeAPI`) ; une action sans contenu finit par `finishAPIAction` (204).
  Aucune logique métier. Le serveur rend des **faits** (instants ISO en
  UTC, durées en secondes), jamais du texte formaté : « vu il y a 12 s » se
  calcule dans le navigateur.
- **Une lecture, une forme** pour l'API et MCP : les assembleurs vivent dans
  `server` et servent les deux (`probeListOf`, `containerListOf`,
  `probeResponseOf`, `containerResponseOf`, `gapsResponseOf`,
  `alertResponseOf`). Une liste se convertit par `httpx.ConvertAll(alerts,
  alertToJSON)`, jamais `null` ; `orEmpty` pour une liste déjà faite,
  `httpx.TimeOrNil` pour un instant zéro.
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
  chaque clé demandée existe dans les deux langues. **Et aucune clé sans
  lecteur** : le test inverse (`TestCatalog_EveryKeyHasAReader`) refuse une
  clé que ni le front, ni un composant, ni une commande ne nomme en entier,
  par une valeur d'une liste fermée ou par un préfixe composé
  (`` `job.snippet_${key}` ``). Une clé morte se retire des deux catalogues.
  Les chaînes sont des formats Go (`%s`, `%d`), lus par le serveur et par le
  front : un pourcentage s'écrit `%%`, un test refuse un `%` seul.
- Une page lit ses données par les hooks de `api/<ressource>.ts`
  (`useProbes()`, `useProbe(id)`, bâtis sur `useResource`, relus sur le
  signal de `useRefresh()`) et écrit par ses fonctions `async`
  (`saveProbe`, `deleteProbe`…). Aucun chemin `/api/` hors de `api/`, sauf
  le flux du direct dans `useLive`.
- **Un seul contrat d'API** : les types de `api/<ressource>.ts` décrivent ce
  que le serveur rend, et `api/contract.test.ts` relit les goldens Go
  (`internal/server/testdata`) pour comparer leurs clés à chaque type, tsc
  vérifiant que la description a exactement les clés du type. Un champ
  renommé en Go casse le test du front ; un golden nouveau y prend son type,
  ou la raison pour laquelle le front ne le lit pas. Un type exporté est
  importé ailleurs ; une brique de son seul fichier ne s'exporte pas.
- Une page qui ne sert qu'au développement (`/systeme-visuel`) passe par
  `import.meta.env.DEV` et un import dynamique : elle n'entre pas dans le
  bundle de production.
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
  `go test ./internal/server -update` pour les réécrire après vérification),
  relues aussi par le front (`web/src/api/contract.test.ts`). Tout
  changement casse un test — c'est voulu.
- **La vraie base** : un composant et ses consommateurs se testent sur une
  SQLite temporaire, ouverte par `storetest.Open(t)`
  (`internal/store/storetest`, avec `EnrollMachine`,
  `EnrollMachineWithKey` quand le test signe comme l'agent, et
  `EnsureLocalMachine`) ; `server` et ses sous-paquets y ajoutent
  `servertest` (`RequestChallenge`, `OpenStream`, l'horloge figée `Now`).
  Un faux seulement pour ce qui est lent ou externe : Docker, un registre,
  un relais SMTP (`mail/smtptest`), un tiers sur Internet.
- **Les gardes de règle** lisent le source : `TestNameOnlyInBrand`, et le
  test des refus (`22-fonctionnement.md`, « Les refus »).
- **Un test instable se corrige par sa cause** : attendre un signal (un
  canal, une condition relue), fixer l'ordre de ce qui se croise. Jamais un
  `time.Sleep` de plus ni un délai allongé pour « laisser le temps ». Le
  test avance dès le signal ; la borne de l'attente est large (30 s) et ne
  sert qu'à ne pas bloquer la CI si le code est cassé.
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
