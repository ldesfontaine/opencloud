package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/update"
	"github.com/ldesfontaine/opencloud/internal/version"
)

// components sont les composants que serve et mcp construisent de la même
// façon, déjà branchés entre eux : un composant ajouté se branche ici, une
// fois. Ce qui ne vaut que pour serve (le direct, les boucles, les
// commandeurs, la machine openCloud) reste dans serve.go.
type components struct {
	db          *store.DB
	logger      *slog.Logger
	preferences *settings.Service
	machines    *machine.Service
	heartbeats  *heartbeat.Service
	resources   *resource.Service
	containers  *container.Tracker
	probes      *probe.Service
	declared    *directory.Directory
	updates     *update.Service
	statusPage  *status.Service
	alerts      *alert.Engine
	notifier    *alert.Notifier
	access      *mcp.Service
	operator    *auth.Service
	support     *lifecycle.Service
	manifests   *manifest.Manifest
}

// buildComponents construit les composants sur la base ouverte. Rien n'y
// tourne encore : aucune boucle, aucune écriture.
func buildComponents(cfg config.Config, stateDir *os.Root, db *store.DB, logger *slog.Logger) (*components, error) {
	// Les réglages de l'opérateur : relus dans la base à chaque usage.
	preferences := settings.New(db, logger)
	machines := machine.New(db, machine.NewSessions(), logger)
	heartbeats := heartbeat.New(db, logger)
	resources := resource.New(db, logger)
	containers := container.New(db, logger)
	probes := probe.New(db, logger)
	declared := directory.New(db, logger)
	updates := update.New(db, logger)
	// L'annuaire lit ce que les machines montrent ; le suivi des conteneurs lui
	// demande les unités à chaque rapport, et l'annuaire lui demande de
	// rattacher de nouveau après chaque écriture.
	declared.SetObserved(containers, updates)
	declared.SetRematcher(containers)
	// La politique de mise à jour vit sur l'unité : l'annuaire dit au suivi
	// des images de redire à la machine ce qu'elle ne doit plus interroger.
	declared.SetAssigner(updates)
	containers.SetUnits(declared)
	statusPage := status.New(db, db, machines, logger)
	// Les alertes : chaque composant lui donne ses faits, le notifieur livre
	// aux canaux dans la langue de l'interface.
	catalogs, err := lang.Load()
	if err != nil {
		return nil, fmt.Errorf("load languages: %w", err)
	}
	alerts := alert.New(db, logger)
	alerts.SetDiskThresholds(preferences.CurrentDiskThresholds)
	notifier := alert.NewNotifier(db, catalogs, preferences.CurrentLanguage, logger)
	notifier.SetPublicURL(cfg.PublicURL)
	notifier.SetMail(preferences.CurrentMailRelay, mail.NewSender(alert.MailTimeout))
	alerts.SetSender(notifier)
	machines.SetAlerter(alerts)
	heartbeats.SetAlerter(alerts)
	resources.SetAlerter(alerts)
	containers.SetAlerter(alerts)
	probes.SetAlerter(alerts)
	// L'accès d'un agent IA : le client OAuth, ses jetons, leur purge.
	access := mcp.New(db, logger)
	// L'accès de l'opérateur à l'interface : relu à chaque requête.
	operator, err := auth.New(db, logger)
	if err != nil {
		return nil, err
	}
	// Le journal d'audit : chaque composant y écrit ses gestes, sous l'acteur
	// que le contexte porte. Les alertes le purgent avec leur historique.
	journal := audit.New(db, logger)
	preferences.SetAuditor(journal)
	machines.SetAuditor(journal)
	heartbeats.SetAuditor(journal)
	probes.SetAuditor(journal)
	declared.SetAuditor(journal)
	statusPage.SetAuditor(journal)
	alerts.SetAuditor(journal)
	access.SetAuditor(journal)
	operator.SetAuditor(journal)
	// La fin de support : activée tant que l'opérateur ne la coupe pas sous
	// Paramètres. La machine openCloud lit endoflife.date une fois par jour,
	// deux au plus, pour les produits des images présentes et les systèmes
	// des machines : c'est une boucle de serve.
	support := lifecycle.New(containers.Images, machines.Systems, lifecycle.NewFileCache(stateDir), lifecycle.NewHTTPSource(), preferences.CurrentLifecycleEnabled, logger)
	// Le fichier de l'annuaire lit et écrit par les mêmes composants que
	// l'interface : les agents reçoivent leurs sondes, le rapprochement se
	// refait, les alertes suivent.
	manifests := manifest.New(manifest.Dependencies{
		Directory: declared, Probes: probes, Heartbeats: heartbeats, Reader: db, Journal: db,
	}, logger)
	return &components{
		db: db, logger: logger, preferences: preferences,
		machines: machines, heartbeats: heartbeats, resources: resources, containers: containers,
		probes: probes, declared: declared, updates: updates, statusPage: statusPage,
		alerts: alerts, notifier: notifier, access: access, operator: operator,
		support: support, manifests: manifests,
	}, nil
}

// serverOptions rend les options communes du serveur. Le direct (Live,
// PublicLive) et le secret du mandataire sont à la charge de l'appelant.
func (c *components) serverOptions(cfg config.Config, configPath string) server.Options {
	return server.Options{
		Logger:         c.logger,
		Version:        version.Number(),
		Reader:         c.db,
		Settings:       c.preferences,
		Machines:       c.machines,
		Heartbeats:     c.heartbeats,
		Resources:      c.resources,
		Containers:     c.containers,
		Probes:         c.probes,
		Directory:      c.declared,
		Manifest:       c.manifests,
		Updates:        c.updates,
		Lifecycle:      c.support,
		Status:         c.statusPage,
		Alerts:         c.alerts,
		Notifier:       c.notifier,
		MCP:            c.access,
		Auth:           c.operator,
		PublicURL:      cfg.PublicURL,
		Listen:         cfg.Listen,
		TrustedProxies: cfg.TrustedPrefixes(),
		ConfigPath:     configPath,
	}
}
