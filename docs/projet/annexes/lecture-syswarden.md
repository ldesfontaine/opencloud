# Lecture du code de SysWarden — ce qu'on en retient pour le développement

Source : github.com/duggytuxy/syswarden, v4.04.2, Go, ~136 k lignes. Pas
utilisé comme WAF (CrowdSec retenu). Lu pour ses façons de faire.

## À reprendre

| Façon de faire | Où dans SysWarden | Pour openCloud |
|---|---|---|
| Config chargée à part, décodée, validée, puis seulement publiée sous mutex | `syswarden-core/config/config.go:227-354` | Recharger la conf sans état à moitié appliqué |
| Clés de conf inconnues détectées par réflexion sur les tags de struct | `config.go:795-826` | Une faute de frappe = un avertissement, pas un silence |
| Écriture atomique : tmp aléatoire, write, fsync, rename, fsync du dossier, refus si la cible a changé | `syswarden-cli/config/migrator.go:565` | Scripts déposés, fichiers d'état hors SQLite |
| `os.Root` (Go 1.24+) pour tout accès sous un répertoire fixe | `config.go:426`, `firewall/manager_linux.go:38` | `/srv/data/opencloud`, scripts envoyés |
| **Journal de transaction par mutation, relu au démarrage** (`prepared` → `committed` / `rolled_back`) | `nft_recovery_linux.go:402`, `cronstate/state_linux.go` | **Le modèle de la file d'actions** : préparer, journaliser, appliquer, vérifier, nettoyer ; reprise déterministe après coupure |
| Vérifier à blanc, appliquer, relire l'état réel et comparer | `nft_transaction_linux.go:891-906` | Chaque script : `--check`, exécution, relecture |
| Marqueur « opération en cours » qui bloque toute autre mutation | `syswarden-core/main.go:31-100` | Un marqueur maintenance par machine qui gèle sa file |
| L'affichage lit un instantané JSON écrit par le daemon | `telemetry/worker.go:897` | Le rendu web lit un instantané, n'interroge pas les exécuteurs |
| Unités systemd durcies écrites par le programme | `pkg/system/service_linux.go:93-125` | `opencloud.service` et les unités déposées |
| Mise à jour signée : clés Ed25519 embarquées, manifeste signé, SHA-256 par artefact, jamais de repli non signé | `pkg/system/upgrade.go` | `opencloud self-update` — plus tard |
| Téléchargements épinglés : HTTPS, SHA-256 déclaré, taille bornée, staging + rename | `pkg/network/downloader.go:612-700` | Tout ce qu'openCloud télécharge |
| Contrats figés en fixtures (arbre CLI, JSON) | `cmd/contract_snapshot_test.go` | Protéger l'interface et l'API interne à bas coût |
| Marqueur anti-boucle dans les journaux | `logger.go:38-57` | openCloud relit journald : distinguer ses traces de celles des scripts |
| Binaire reproductible : `CGO_ENABLED=0`, `-trimpath`, `SOURCE_DATE_EPOCH` | `build_packages.sh:7-80` | Compatible SQLite pur Go (modernc) |

## À ne pas reprendre

- Trois binaires et Viper global partout : un binaire, une struct de config passée explicitement.
- `/etc/cron.d` sécurisé à grands frais (2 000 lignes) : minuteries systemd, déjà tranché.
- `bash -c "tail -F … & journalctl -f"` : `journalctl -o json --follow` ou une bibliothèque.
- Paranoïa TOCTOU (double `Lstat` à chaque lecture) : `os.Root` + modes suffisent.
- `Type=simple` sans `sd_notify` ni watchdog : openCloud fait mieux (`Type=notify`).
- Pas de rechargement à chaud (`reload` = restart).

## Bibliothèques

- `spf13/cobra` : CLI ; `PersistentPreRunE` comme garde globale.
- `pelletier/go-toml/v2` seul, sans Viper : conf TOML typée.
- `go-playground/validator/v10` : validation par tags.
- `golang.org/x/sys/unix` : `openat`, `renameat2`, `flock` — nécessaire à l'atomicité.
- Absents chez SysWarden, à choisir ailleurs : SSH (`golang.org/x/crypto/ssh`), SQLite (`modernc.org/sqlite`), systemd (`coreos/go-systemd`).
