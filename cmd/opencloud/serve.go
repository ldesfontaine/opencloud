package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/proxysecret"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/version"
)

const (
	readHeaderTimeout = 10 * time.Second
	// Une connexion gardée ouverte sans requête se ferme au bout de deux
	// minutes ; un flux en direct n'est pas concerné, il a une requête.
	idleTimeout     = 2 * time.Minute
	shutdownTimeout = 10 * time.Second
	stateDirMode    = 0o700
	// Le défaut de net/http est 1 Mio d'en-têtes par requête, et /ping/ et
	// /statut sont ouverts : un anonyme pourrait y mettre une URL d'un mégaoctet.
	// Les en-têtes légitimes (Authorization Basic ou Bearer, X-Forwarded-For,
	// preuve signée de l'agent) tiennent en quelques centaines d'octets.
	// L'interface ne pose aucun cookie ; ceux qu'un mandataire
	// d'authentification pose sur le domaine parent arrivent aussi, mais
	// pèsent quelques Kio : la marge reste large.
	maxHeaderBytes = 64 << 10
)

func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", config.DefaultPath, "chemin du fichier de configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, warnings, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	for _, warning := range warnings {
		logger.Warn("config", "detail", warning)
	}
	// Le secret du mandataire avant tout le reste : un fichier faux ou trop ouvert ne doit
	// jamais laisser le mode forward croire une adresse faute de mieux.
	forwardSecret, err := proxysecret.Load(cfg.ForwardSecretFile)
	if err != nil {
		return err
	}
	stateDir, err := openStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	defer stateDir.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Ce que serve fait de lui-même, au démarrage et dans ses boucles, va au
	// journal d'audit sous « rule » ; une requête a son propre contexte.
	ctx = audit.WithActor(ctx, audit.Rule(""))

	db, err := store.Open(ctx, stateDir)
	if err != nil {
		return err
	}
	defer db.Close()
	// La clé avant tout ce qui lit un secret.
	if err := openSecrets(ctx, stateDir, db, logger); err != nil {
		return err
	}
	// La clé qui signe les ordres aux agents, créée au premier démarrage.
	serverKey, err := openServerKey(stateDir, logger)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := db.PurgeTokens(ctx, now, now.Add(-machine.ConsumedTokenRetention)); err != nil {
		logger.Warn("purge enrollment tokens", "error", err)
	}
	// Les composants, branchés entre eux ; tout ce qui suit n'appartient
	// qu'à serve.
	parts, err := buildComponents(cfg, stateDir, db, logger)
	if err != nil {
		return err
	}

	// Le bus du direct : les composants y publient, les onglets y lisent. La
	// page de statut écoute le bus interne pour relire, et publie sur les
	// deux bus quand quelque chose de visible change.
	bus := live.New()
	publicBus := live.New()
	parts.machines.SetWatcher(bus)
	parts.heartbeats.SetWatcher(bus)
	parts.resources.SetWatcher(bus)
	parts.containers.SetWatcher(bus)
	parts.probes.SetWatcher(bus)
	parts.declared.SetWatcher(bus)
	parts.updates.SetWatcher(bus)
	parts.statusPage.AddWatcher(bus)
	parts.statusPage.AddWatcher(publicBus)
	parts.statusPage.SetFeed(bus)
	parts.alerts.SetWatcher(bus)
	parts.support.SetWatcher(bus)
	parts.manifests.SetWatcher(bus)
	parts.actions.SetWatcher(bus)
	parts.sshKeys.SetWatcher(bus)
	parts.actions.SetKey(serverKey)
	// Les colis des ordres longs (la sauvegarde) : un dossier vidé à chaque
	// démarrage de serve.
	outbox, err := action.NewOutbox(stateDir)
	if err != nil {
		return err
	}
	parts.actions.SetOutbox(outbox)
	parts.backups.SetParcels(outbox)
	parts.backups.SetWatcher(bus)

	// serve n'observe rien de sa propre machine : c'est l'agent installé sur
	// elle, comme sur les autres. L'empreinte de la machine ne sert qu'à le
	// reconnaître à l'enrôlement, pour qu'il prenne l'identifiant stable de
	// la machine openCloud et son historique.
	hostname, _ := os.Hostname()
	parts.machines.SetServerHost(hostinfo.HostFingerprint(), hostname)
	offerLocalEnrollment(ctx, logger, parts)

	logInterfaceAccess(ctx, logger, parts.operator, cfg.Listen)
	warnIfForwardUnproven(ctx, logger, parts.operator, cfg.TrustedPrefixes(), forwardSecret != "")
	// Treize boucles de fond : les échéances, le rollup et la purge des
	// mesures, la purge des conteneurs, le rollup et la purge des sondes, la
	// purge des constats d'images, la page de statut, les machines perdues,
	// le moteur des alertes, le notifieur, la purge des jetons d'API, la fin
	// de support, l'expiration et la purge des ordres, la sauvegarde de la
	// base. Elles finissent avant
	// que la base ne se ferme.
	var loops sync.WaitGroup
	runLoop(&loops, func() { parts.heartbeats.Watch(ctx) })
	runLoop(&loops, func() { parts.resources.Watch(ctx) })
	runLoop(&loops, func() { parts.containers.Watch(ctx) })
	runLoop(&loops, func() { parts.probes.Watch(ctx) })
	runLoop(&loops, func() { parts.updates.Watch(ctx) })
	runLoop(&loops, func() { parts.statusPage.Watch(ctx) })
	runLoop(&loops, func() { parts.machines.Watch(ctx) })
	runLoop(&loops, func() { parts.alerts.Watch(ctx) })
	runLoop(&loops, func() { parts.notifier.Run(ctx) })
	runLoop(&loops, func() { parts.apiTokens.Watch(ctx) })
	runLoop(&loops, func() { parts.support.Watch(ctx) })
	runLoop(&loops, func() { parts.actions.Watch(ctx) })
	runLoop(&loops, func() { parts.backups.Watch(ctx) })
	defer func() { stop(); loops.Wait() }()
	// Une sonde dont la cible porte des identifiants, d'avant leur refus, ne
	// repart pas : elle attend que l'opérateur change sa cible.
	if err := parts.probes.PauseTargetsWithCredentials(ctx); err != nil {
		logger.Warn("pause probes with credentials", "error", err)
	}
	// Les services saisis avant que le rapprochement existe, ou pendant que
	// le serveur était arrêté, sont rattachés sans attendre l'inventaire
	// suivant de chaque agent. Les exclusions se disent après : c'est
	// l'unité rattachée qui porte la politique.
	parts.containers.RematchAll(ctx)

	// L'arrêt d'un apply suit celui du serveur : un apply en cours finit son
	// changement puis se journalise « interrompu ».
	parts.manifests.SetLifetime(ctx)
	// Un passage resté « en cours » est celui d'un arrêt brutal : plus rien
	// ne l'écrit, il passe en échec pour que le journal dise vrai.
	if _, err := parts.manifests.FailInterrupted(ctx); err != nil {
		logger.Warn("mark interrupted applies", "error", err)
	}

	options := parts.serverOptions(cfg)
	options.Live = bus
	options.PublicLive = publicBus
	options.ForwardSecret = forwardSecret
	server, err := server.New(options)
	if err != nil {
		return err
	}
	// Le serveur tient les flux des agents : c'est lui qui leur commande.
	parts.containers.SetCommander(server)
	parts.probes.SetCommander(server)
	parts.updates.SetCommander(server)
	parts.actions.SetCommander(server)
	// Le port principal s'ouvre avant la socket de commande : un second serve
	// sur le même state_dir échoue ici, avant d'avoir touché à la socket du
	// premier.
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	control := startControlSocket(logger, stateDir, cfg.StateDir, server.ControlHandler())
	return listenUntilSignal(ctx, logger, newHTTPServer(cfg.Listen, server), listener, control, cfg)
}

// logInterfaceAccess dit au journal comment l'interface est gardée. Sans
// compte, elle attend sa première configuration : sur la boucle locale, c'est
// une information ; au-delà, un avertissement, car le premier qui atteint
// l'adresse crée le compte.
func logInterfaceAccess(ctx context.Context, logger *slog.Logger, operator *auth.Service, listen string) {
	state, err := operator.State(ctx)
	if err != nil {
		logger.Warn("read interface authentication", "error", err)
		return
	}
	if state.Mode != auth.ModeNone {
		logger.Info("interface authentication", "mode", string(state.Mode), "username", state.Username, "header", state.Header)
		return
	}
	if isLoopbackListen(listen) {
		logger.Info("interface awaiting first setup: open it in a browser and create the operator account", "listen", listen)
		return
	}
	logger.Warn("interface awaiting first setup and listening beyond loopback: whoever reaches this address first creates the operator account; open it and create it now",
		"listen", listen)
}

// warnIfForwardUnproven prévient quand le mode forward est actif sans que rien
// prouve le passage par le mandataire : pas de secret, et un préfixe de
// confiance couvre la boucle locale, donc tous les processus de la machine.
// La config seule ne peut pas le dire, le mode est dans la base ; c'est ici
// que les deux sont connus. L'accès n'est pas coupé : ce serait enfermer
// l'opérateur d'une installation existante.
func warnIfForwardUnproven(ctx context.Context, logger *slog.Logger, operator *auth.Service, trusted []netip.Prefix, hasSecret bool) {
	if hasSecret || !server.TrustsLoopback(trusted) {
		return
	}
	state, err := operator.State(ctx)
	if err != nil || state.Mode != auth.ModeForward {
		return
	}
	logger.Warn("forward authentication has no proof of the proxy: a loopback trusted proxy covers every process of this machine, any of them can send the identity header and be the operator; set forward_secret_file and have the proxy send the secret",
		"header", state.Header, "secret_header", brand.ProxySecretHeader)
}

// isLoopbackListen : « 127.0.0.1:8080 », « [::1]:8080 », « localhost:8080 ».
// Une adresse vide (« :8080 ») écoute partout.
func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

func runLoop(loops *sync.WaitGroup, loop func()) {
	loops.Add(1)
	go func() {
		defer loops.Done()
		loop()
	}()
}

// Le répertoire d'état est ouvert une fois par os.Root ; les composants qui
// y écrivent passent par lui.
func openStateDir(path string) (*os.Root, error) {
	if err := os.MkdirAll(path, stateDirMode); err != nil { // bounded: config.StateDir
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open state dir: %w", err)
	}
	return root, nil
}

// newHTTPServer prépare le serveur HTTP. Les flux en direct (agents,
// interface, journaux, statut) ne finissent qu'avec le contexte de
// leur requête : il dérive d'un contexte de base annulé dès l'arrêt, sans
// quoi Shutdown les attendrait jusqu'à son délai. Une requête courte en
// cours au même instant voit aussi son contexte annulé : sa transaction
// est annulée, l'agent ou le navigateur la rejoue.
//
// Pas de ReadTimeout : net/http annule le contexte d'une requête dont la
// lecture expire, même une fois le corps lu, ce qui couperait tous les
// flux. Un corps lent reste borné en taille (1 Mio) et Traefik, devant,
// tient ses propres délais de lecture.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	base, cancelBase := context.WithCancel(context.Background())
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	httpServer.RegisterOnShutdown(cancelBase)
	return httpServer
}

// listenUntilSignal sert sur listener jusqu'au signal d'arrêt. control, la
// socket de commande, est nil quand elle n'a pas pu s'ouvrir.
func listenUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, listener net.Listener, control *http.Server, cfg config.Config) error {
	if control != nil {
		// Fermer retire aussi le fichier de la socket, quelle que soit la
		// sortie.
		defer control.Close()
	}
	failed := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "state_dir", cfg.StateDir, "version", version.String())
		failed <- httpServer.Serve(listener)
	}()

	select {
	case err := <-failed:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// La socket de commande s'arrête en même temps que le serveur principal,
	// pas avant lui : une requête de commande lente ne doit pas manger le
	// délai du reste.
	controlDone := make(chan error, 1)
	if control != nil {
		go func() { controlDone <- control.Shutdown(shutdownCtx) }()
	} else {
		controlDone <- nil
	}
	mainErr := httpServer.Shutdown(shutdownCtx)
	if err := <-controlDone; err != nil {
		logger.Warn("shutdown control socket", "error", err)
	}
	if mainErr != nil && !errors.Is(mainErr, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", mainErr)
	}
	return nil
}

// offerLocalEnrollment émet, tant que la machine openCloud n'a jamais eu
// d'agent, le jeton qui l'installe : c'est la première étape de
// l'installation. La ligne se lit dans l'interface, à la première
// configuration ; le journal dit seulement qu'elle attend, jamais le jeton.
func offerLocalEnrollment(ctx context.Context, logger *slog.Logger, parts *components) {
	_, token, err := parts.machines.OfferLocalEnrollment(ctx)
	if errors.Is(err, machine.ErrLocalEnrolled) {
		return
	}
	if err != nil {
		logger.Warn("offer the agent of this machine", "error", err)
		return
	}
	catalogs, err := lang.Load()
	if err != nil {
		logger.Warn("load languages", "error", err)
		return
	}
	text := catalogs.For(parts.preferences.CurrentLanguage())
	logger.Info("enrollment token waiting for the agent of this machine", "message", text.Get("machine.local_agent_waiting"),
		"name", token.Name, "expires_at", token.ExpiresAt.UTC())
}
