package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// #nosec G101 -- le texte d'aide de la commande, pas un secret.
const secretsUsage = `Usage : {command} secrets <sous-commande> [-config chemin]

  forget              clé perdue : crée une clé neuve et oublie ce que
                      l'ancienne chiffrait (mot de passe du relais, URL et
                      secret des canaux, jetons de ping). Refuse si la clé
                      ouvre encore tout. Arrêtez le serveur avant
  rotate              tire une clé de chiffrement neuve et rescelle chaque
                      secret avec elle ; l'ancienne est mise de côté.
                      Arrêtez le serveur avant
  rotate-server-key   tire une clé neuve pour signer les ordres ; la
                      précédente reste valable le temps que chaque agent
                      reçoive la neuve. Prend effet au redémarrage
  finish-server-key   retire la clé précédente une fois chaque agent passé
                      à la neuve (option -force : sans attendre les agents
                      restés dessus, qui seront à ré-enrôler)
`

// openSecrets donne à la base la clé du serveur, puis vérifie qu'elle ouvre
// chaque secret : une base qui ne s'ouvre pas avec elle est refusée dès le
// démarrage, pas à la première alerte. serve peut créer la clé ; mcp ne la
// crée jamais : il lit la base d'un serveur qui existe déjà.
func openSecrets(ctx context.Context, stateDir *os.Root, db *store.DB, logger *slog.Logger, mayCreate bool) error {
	keyring, err := loadOrCreateKey(ctx, stateDir, db, logger, mayCreate)
	if err != nil {
		return err
	}
	db.SetSealer(keyring)
	return verifySecrets(ctx, db)
}

func loadOrCreateKey(ctx context.Context, stateDir *os.Root, db *store.DB, logger *slog.Logger, mayCreate bool) (*seal.Keyring, error) {
	keyring, err := loadKey(stateDir)
	if errors.Is(err, seal.ErrNoKey) {
		return createKey(ctx, stateDir, db, logger, mayCreate)
	}
	return keyring, err
}

// loadKey lit la clé ; ErrNoKey passe tel quel, les autres échecs deviennent
// des refus que l'opérateur sait lire.
func loadKey(stateDir *os.Root) (*seal.Keyring, error) {
	keyring, err := seal.Load(stateDir)
	switch {
	case err == nil, errors.Is(err, seal.ErrNoKey):
		return keyring, err
	case errors.Is(err, seal.ErrKeyFileMode):
		keyPath := filepath.Join(stateDir.Name(), brand.SecretKeyFile)
		return nil, &refusal{message: fmt.Sprintf("%s est lisible par d'autres comptes : réservez-le à son propriétaire (chmod 600 %s)", brand.SecretKeyFile, keyPath)}
	case errors.Is(err, seal.ErrKeyFileInvalid):
		return nil, &refusal{message: fmt.Sprintf("%s n'est pas une clé intacte (%v) : restaurez la clé sauvegardée avec la base, ou lancez `%s secrets forget` (ce qui est perdu : voir installation.md)",
			brand.SecretKeyFile, err, brand.Command)}
	}
	return nil, fmt.Errorf("load secret key: %w", err)
}

// createKey ne crée la clé que sur une base qui n'a aucun chiffré : sinon la
// neuve chiffrerait les écritures suivantes et la base mêlerait deux clés.
func createKey(ctx context.Context, stateDir *os.Root, db *store.DB, logger *slog.Logger, mayCreate bool) (*seal.Keyring, error) {
	if !mayCreate {
		return nil, &refusal{message: fmt.Sprintf("%s absent : démarrez serve une fois, ou restaurez la clé", brand.SecretKeyFile)}
	}
	hasSealed, err := db.HasSealedSecrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("check sealed secrets: %w", err)
	}
	if hasSealed {
		return nil, &refusal{message: fmt.Sprintf("%s contient des secrets chiffrés mais %s est absent de state_dir : restaurez la clé sauvegardée avec la base, ou lancez `%s secrets forget` (ce qui est perdu : voir installation.md)",
			brand.DatabaseFile, brand.SecretKeyFile, brand.Command)}
	}
	keyring := seal.Generate()
	err = seal.Create(stateDir, keyring)
	if errors.Is(err, seal.ErrKeyExists) {
		// Un autre serveur lancé en même temps l'a posée avant nous : on
		// chiffre avec la sienne, la nôtre n'a jamais servi.
		return loadKey(stateDir)
	}
	if err != nil {
		return nil, fmt.Errorf("save secret key: %w", err)
	}
	logger.Info("secret key created", "file", brand.SecretKeyFile)
	return keyring, nil
}

func verifySecrets(ctx context.Context, db *store.DB) error {
	err := db.VerifySealedSecrets(ctx)
	if errors.Is(err, store.ErrSecretUnreadable) {
		return &refusal{message: fmt.Sprintf("un secret de la base ne s'ouvre pas avec %s (%v) : ce n'est pas la clé de cette base. Restaurez celle sauvegardée avec elle, ou lancez `%s secrets forget` (ce qui est perdu : voir installation.md)",
			brand.SecretKeyFile, err, brand.Command)}
	}
	if err != nil {
		return fmt.Errorf("verify sealed secrets: %w", err)
	}
	return nil
}

// opencloud secrets forget : la porte de secours quand la clé est perdue.
// Elle écrit dans la base sous le compte du service : qui peut la lancer peut
// déjà lire state_dir, elle n'ouvre rien de plus.
func runSecrets(args []string) error {
	return secretsCommand(args, os.Stdout)
}

func secretsCommand(args []string, out io.Writer) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, strings.ReplaceAll(secretsUsage, "{command}", brand.Command))
		return &refusal{message: "sous-commande manquante"}
	}
	command := args[0]
	if !slices.Contains([]string{"forget", "rotate", "rotate-server-key", "finish-server-key"}, command) {
		fmt.Fprint(os.Stderr, strings.ReplaceAll(secretsUsage, "{command}", brand.Command))
		return &refusal{message: "sous-commande inconnue : " + command}
	}
	flags := flag.NewFlagSet("secrets "+command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", config.DefaultPath, "chemin du fichier de configuration")
	force := flags.Bool("force", false, "finish-server-key : retirer la clé précédente même si des agents y sont restés")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	cfg, _, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	stateDir, err := openStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	defer stateDir.Close()
	ctx := audit.WithActor(context.Background(), audit.CLI(currentAccount()))
	db, err := store.Open(ctx, stateDir)
	if err != nil {
		return err
	}
	defer db.Close()
	switch command {
	case "rotate":
		return rotateSecrets(ctx, stateDir, db, out)
	case "rotate-server-key":
		return rotateServerKey(ctx, stateDir, db, out)
	case "finish-server-key":
		return finishServerKey(ctx, stateDir, db, *force, out)
	}
	return forgetSecrets(ctx, stateDir, db, out)
}

// commandJournal est le journal d'audit d'une commande locale : ses propres
// échecs d'écriture vont sur la sortie d'erreur.
func commandJournal(db *store.DB) *audit.Recorder {
	return audit.New(db, slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
}

// forgetSecrets met l'ancienne clé de côté, en pose une neuve, puis vide ce
// qu'elle chiffrait. Si la seconde étape échoue, la relancer recommence : la
// clé neuve n'ouvre toujours rien, la commande ne se croit donc pas inutile.
func forgetSecrets(ctx context.Context, stateDir *os.Root, db *store.DB, out io.Writer) error {
	if err := refuseWhenNothingIsLost(ctx, stateDir, db); err != nil {
		return err
	}
	keptAs, err := setAsideOldKey(stateDir)
	if err != nil {
		return err
	}
	keyring := seal.Generate()
	err = seal.Create(stateDir, keyring)
	if errors.Is(err, seal.ErrKeyExists) {
		return &refusal{message: fmt.Sprintf("%s est réapparu pendant la commande : un serveur tourne encore, arrêtez-le d'abord", brand.SecretKeyFile)}
	}
	if err != nil {
		return fmt.Errorf("save secret key: %w", err)
	}
	db.SetSealer(keyring)
	channels, tokens, err := db.ForgetSecrets(ctx, heartbeat.NewToken)
	if err != nil {
		return err
	}
	// Des secrets viennent d'être effacés et des URL de ping de changer : le
	// journal d'audit le garde.
	commandJournal(db).Record(ctx, audit.Event{Action: audit.SecretsForget, Details: audit.Details{
		"channels_disabled": strconv.Itoa(channels), "ping_tokens_renewed": strconv.Itoa(tokens),
	}})
	keyPath := filepath.Join(stateDir.Name(), brand.SecretKeyFile)
	if _, err := fmt.Fprintf(out, "Nouvelle clé écrite : %s\n", keyPath); err != nil {
		return err
	}
	if keptAs != "" {
		fmt.Fprintf(out, "Ancienne clé gardée : %s (elle n'ouvre plus rien ici ; elle sert à relire une ancienne sauvegarde de la base)\n", filepath.Join(stateDir.Name(), keptAs))
	}
	_, err = fmt.Fprintf(out, "mot de passe du relais : effacé\ncanaux désactivés : %d\njetons de ping renouvelés : %d\n"+
		"Redémarrez le serveur, ressaisissez ces secrets sous Paramètres et mettez à jour les URL de ping des crons.\n",
		channels, tokens)
	return err
}

// refuseWhenNothingIsLost protège d'un faux pas : une clé présente et intacte
// qui ouvre déjà tous les chiffrés n'est pas perdue, et la commande
// effacerait des secrets qu'on peut encore lire.
func refuseWhenNothingIsLost(ctx context.Context, stateDir *os.Root, db *store.DB) error {
	current, err := seal.Load(stateDir)
	if err != nil {
		// Absente, trop ouverte ou abîmée : c'est justement le cas de la commande.
		return nil
	}
	db.SetSealer(current)
	err = db.VerifySealedSecrets(ctx)
	if errors.Is(err, store.ErrSecretUnreadable) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify sealed secrets: %w", err)
	}
	return &refusal{message: fmt.Sprintf("%s est intacte et ouvre tous les secrets de la base : il n'y a rien à oublier, rien n'a été changé", brand.SecretKeyFile)}
}

// setAsideOldKey renomme la clé en place plutôt que de l'écraser : une
// sauvegarde plus ancienne de la base ne se relit qu'avec elle. Rend le nouveau
// nom, vide s'il n'y avait pas de clé.
func setAsideOldKey(stateDir *os.Root) (string, error) {
	if _, err := stateDir.Lstat(brand.SecretKeyFile); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("stat secret key: %w", err)
	}
	keptAs := brand.SecretKeyFile + ".forgotten-" + time.Now().UTC().Format("20060102T150405Z")
	if err := stateDir.Rename(brand.SecretKeyFile, keptAs); err != nil {
		return "", fmt.Errorf("set aside old secret key: %w", err)
	}
	return keptAs, nil
}
