package firewall

import (
	"context"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// exportTimeout borne la lecture de la configuration : un seul appel, mais
// le fichier est plus gros qu'un relevé.
const exportTimeout = 2 * time.Minute

// ExportPrivilege est le libellé, tel que le pare-feu l'écrit dans son écran
// Système → Accès → Utilisateurs, du droit que la clé d'API doit avoir pour
// que l'export de la configuration réponde.
const ExportPrivilege = "Diagnostics: Configuration History"

// ErrExportForbidden : la clé répond (les relevés passent) mais l'export de la
// configuration lui est refusé. Le droit manquant est ExportPrivilege.
var ErrExportForbidden = refusal.Invalid("firewall.export_forbidden", "the API key may read the firewall but not export its configuration: it needs the privilege "+ExportPrivilege)

// Exporter est un connecteur qui sait lire la configuration complète de son
// pare-feu. Il est à part de Connector : tous les genres n'ont pas d'export, et
// Connector garde ainsi sa liste de lectures du relevé.
type Exporter interface {
	// ExportConfiguration rend le fichier de configuration tel que le
	// pare-feu le livre : il contient des secrets (empreintes de mots de
	// passe, clés VPN), il ne se journalise ni ne s'affiche.
	ExportConfiguration(ctx context.Context) ([]byte, error)
}

// ExportConfiguration lit la configuration du pare-feu déclaré. Elle ne
// quitte pas le serveur autrement que chiffrée par le producteur de
// sauvegarde ; la clé d'API ne la suit pas.
func (s *Service) ExportConfiguration(ctx context.Context, id string) ([]byte, error) {
	credentials, err := s.store.GetFirewallCredentials(ctx, id)
	if err != nil {
		return nil, err
	}
	if credentials.APISecret == "" {
		return nil, ErrUnauthorized
	}
	connector, err := s.connectorFor(credentials)
	if err != nil {
		return nil, err
	}
	exporter, canExport := connector.(Exporter)
	if !canExport {
		return nil, ErrKindInvalid
	}
	exportCtx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	content, err := exporter.ExportConfiguration(exportCtx)
	if err != nil {
		return nil, fmt.Errorf("export configuration: %w", err)
	}
	return content, nil
}

// Declared rend les pare-feu déclarés, par nom : ce que la sauvegarde propose
// comme sources.
func (s *Service) Declared(ctx context.Context) ([]Summary, error) {
	return s.store.ListFirewalls(ctx)
}
