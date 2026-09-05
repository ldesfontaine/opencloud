# Le code de your-cloud à reprendre

Vérifié par lecture des `import` et `wc -l`, dépôt `/home/lucas/Documents/dev/your-cloud`.
Le couplage tient en une phrase : **`internal/plan` (enveloppes signées) est
importé partout mais presque jamais utilisé** — la plupart des « adapter »
sont des « copier moins trois lignes ». Ce qui ne se reprend pas l'est par
décision de produit (Quadlet, podman rootless, signatures, Tauri), pas par
défaut du code.

Verdicts : **copier** · **adapter** (surface changée) · **porter** (Rust ou
shell → Go ou `run.sh`) · **réécrire** (l'idée) · **laisser**.

> **Ce document est une carte, pas un engagement.** Chaque fichier est
> **relu et réévalué au moment où on le reprend**, dans le contexte de
> l'étape en cours. Un verdict d'ici peut changer à la lecture.

## Par étape de développement (`16-architecture.md`)

### 1 — Squelette

| Fichier | Lignes | Verdict | Pour |
|---|---|---|---|
| `internal/securefile/root_owned.go` | 74 | copier | Lire le jeton Cloudflare et les secrets : fichier régulier, root, non lisible par un tiers, borné |
| `internal/identifier/uuid.go` | 42 | copier | Identifiant d'action |
| `internal/strictjson/strictjson.go` | 240 | copier | JSON strict, clés dupliquées refusées — pour le `compose` fourni |

### 2 — Une action en local · **le cœur**

| Fichier | Lignes | Verdict | Pour |
|---|---|---|---|
| `internal/auxiliary/system.go` L246-282, L1379-1420 | 80 | **copier** | **L'écriture atomique** de tout openCloud : `O_EXCL\|O_NOFOLLOW`, `fsync`, `rename`, `fsync` du dossier. Le premier fichier à prendre |
| `internal/auxiliary/unit.go` L103-142 `environmentLine()` | 40 | **copier** | **Le quoting de `params.env`** : `%` doublé, guillemets si espace. Le défaut qu'il corrige — `TITLE=Your Cloud` lu comme `TITLE=Your`, sans erreur — est exactement le piège d'`EnvironmentFile=` |
| `system.go` L1753-1766 `run()` | 14 | copier | `exec.CommandContext`, `Dir=/`, environnement remplacé — pour `transport/local` |
| `internal/plan/schema2.go` L279-306, L1509-1620 | ~150 | adapter | Regex et bornes (hôte, slot, digest, port) → `internal/validate`. Les 1480 autres lignes servent la signature, écartée |
| `internal/servicedefinition/definition.go` | 669 | adapter | `ValidateSlug`, `validateContainerPath` (anti-remontée), `validateEnvironmentValue` (grammaire d'une valeur d'env) |
| `internal/plan/schema3.go` | 602 | adapter | `validateServicePort`, `decodePeerPublicKey` (32 octets, une orthographe) |
| `internal/auxiliary/apply.go` L1192-1215 | 25 | porter | Le préflight → étape 1 de tout `run.sh` |
| `internal/auxiliary/executor.go` | 449 | adapter | La **forme** seulement : une interface, une mise en œuvre `System`, une fausse en test → `transport` |
| `unit_test.go`, `system_test.go` | 521 + 81 | copier | Les cas d'`environmentLine` et de l'écriture atomique |

### 3 — Deuxième machine

| Fichier | Lignes | Verdict | Pour |
|---|---|---|---|
| `internal/controller/command_launch.go` L455-497 | 42 | **copier** | Le vecteur SSH, option par option, et `known_hosts` dérivé, jamais appris |
| `internal/controller/command_identity.go` | 216 | copier | Paire Ed25519 et encodage OpenSSH sans dépendance — la clé par machine d'`enroll` |
| `tests/lab/v0.1.2/command-path/install-machine` L200-260 | 61 | porter | Le `run.sh` d'*Enrôler* : l'ordre est une propriété de sécurité. Retirer l'ancre et l'anti-rejeu ; le drop-in `sshd_config.d` se copie ligne pour ligne |
| `app/…/machine_identity/entry.rs` `judge()` | 494 | porter | Écrire `authorized_keys`, **relire**, refuser si différent. Jeter `forced_command()` |
| `app/…/machine_identity/elevation_rule.rs` | 532 | porter | Le drop-in sudoers ; garder `env_reset`, `!setenv`, `!log_input`, `!log_stdin` |
| `app/…/personal_access/sudo_policy.rs` | 429 | porter | Préflight `sudo -N -n -l -l` ; « demande un terminal » **séparé de** « demande un secret » |

### 3 bis — Option « derrière NAT »

| Fichier | Lignes | Verdict | Pour |
|---|---|---|---|
| `internal/accessgate/gate.go` + `system.go` | 391 + 381 | **copier** | Passage WireGuard complet, **zéro dépendance interne** : clé née sur la machine, un seul pair, `.netdev`/`.network`. Le seul bloc du dépôt qui se prend en bloc |
| `internal/auxiliary/linkrules.go` | 579 | copier | Une table nftables, un nom, `add`+`delete`+`table` en une transaction, rechargée au démarrage |
| `internal/auxiliary/link.go` L246-265 `sectionAfter()` | 20 | adapter | Ajouter un pair sans réécrire le fichier |

### 4 — Publier

| Fichier | Lignes | Verdict | Pour |
|---|---|---|---|
| `tools/provision-lab` L326-365 | 40 | porter | *Poser le socle* : fichier-garde d'idempotence qui **nomme le plus récent de la liste** — sinon une machine posée tôt garde l'ancienne surface en silence |
| `internal/auxiliary/entrypoint.go` L238-290 | 53 | copier | `files/traefik.yml` de *Installer le proxy*. Ajouter le résolveur DNS-01 |
| `internal/auxiliary/route.go` L91-131 | 41 | copier | Le fragment d'hôte virtuel — **aucune dépendance interne**. Backend : conteneur Docker au lieu de slirp4netns |
| `route.go` L280-345 | 65 | porter | Le `run.sh` de *Créer un hôte virtuel* (déjà au §3 du catalogue) |
| `refusals_test.go`, `route_test.go` | 1501 + 597 | adapter | Un test par refus, chacun prouvant qu'il tombe avant tout effet |

### 5 — Certifier

Rien : your-cloud n'avait pas d'ACME.

### 6 — Services

| Fichier | Lignes | Verdict | Pour |
|---|---|---|---|
| `system.go` L1251-1400 | 150 | **copier** | *Générer les secrets* : 32 octets → hex, `O_EXCL`, jamais remplacé, refus d'une valeur non générée. L'alphabet hex garantit qu'aucun saut de ligne n'entre dans un `.env` |
| `unit.go` L169-225 | 57 | réécrire | Les contrôles Quadlet → `read_only`, `cap_drop`, `security_opt` du gabarit `compose.yaml` |
| `internal/auxiliary/profile.go` | 484 | réécrire | L'idée : chemins dérivés des noms, jamais saisis |
| `app/src-tauri/src/service_definition.rs` L378-833 | 456 | porter — **fort** | Le mode expert : parseur `docker run` / `compose` → formulaire, avec la liste de **ce qui a été retiré**. Le seul « fort » du lot ; pour la fin de l'étape |

### 7 — Sauvegarder

| Fichier | Lignes | Verdict | Pour |
|---|---|---|---|
| `system.go` L1504-1600 | 97 | porter | *Sauvegarder* : refus si slot pris, `tar` vers `.tmp`, digest en flux, `rename` |
| `system.go` L1600-1710 | 110 | porter | *Restaurer* dans **le seul ordre correct** — déballer, archiver l'état remplacé, deux `rename`. Recopier le commentaire tel quel |
| `internal/auxiliary/snapshot.go`, `snapshot_test.go` | 379 + 484 | porter, adapter | Arrêt si nécessaire, remise dans l'état trouvé, preuve que ça répond ; refus mot pour mot |

### 8 — Observer

| Fichier | Lignes | Verdict | Pour |
|---|---|---|---|
| `tests/lab/v0.1.2/clean-removal/census` | 90 | **copier** | Recensement avant/après pour *Retirer une machine* — exclusions nommées et comptées, jamais silencieuses. Remplacer `your-cloud` par `opencloud` |
| `…/clean-removal/compare` | 175 | porter | Diff par zone, constate sans juger → `observe` |
| `internal/buffer/buffer.go` | 519 | réécrire | La borne de taille sur la sortie relue ; SQLite remplace le JSON |

## Laissé

`apply.go` (1685) et `fixtures_test.go` (2036) — enveloppes signées de bout en
bout. `input.go`, `private.go`, `userservice.go`, `linkroute.go` — podman
rootless et topologie à deux machines. `egress.go` — confinement sortant, hors
périmètre pour l'instant.

## À retenir

Le legs le plus dense n'est pas du Go : `census`, `install-machine` et
`provision-lab` — 190 lignes de shell — portent trois motifs qu'openCloud
aurait payé cher à retrouver, et deviennent directement des `run.sh`.
