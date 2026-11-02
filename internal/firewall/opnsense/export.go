package opnsense

import (
	"bytes"
	"context"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/firewall"
)

// Système → Configuration → Sauvegardes : la dernière version enregistrée de
// config.xml (celle que l'écran télécharge). Droit demandé à l'utilisateur
// d'API : « Diagnostics: Configuration History » (firewall.ExportPrivilege,
// page-diagnostics-configurationhistory, motif api/core/backup/*). Les
// relevés passent sans lui : c'est ce qui distingue un 403 d'export d'une clé
// fausse.
const exportPath = "/api/core/backup/download/this"

var configurationRoot = []byte("<opnsense")

// ExportConfiguration lit config.xml. Un 403 est le droit manquant, le seul
// cas où la clé répond ailleurs ; un corps qui n'est pas du XML d'OPNsense
// (page de connexion, JSON d'erreur) n'est pas une sauvegarde.
func (c *Client) ExportConfiguration(ctx context.Context) ([]byte, error) {
	body, err := c.fetch(ctx, exportPath, nil, "application/xml", maxConfigurationBytes)
	if err != nil {
		if isStatus(err, 403) {
			return nil, fmt.Errorf("%w: %w", firewall.ErrExportForbidden, err)
		}
		return nil, err
	}
	if !bytes.Contains(body[:min(len(body), 1024)], configurationRoot) {
		return nil, fmt.Errorf("%w: the answer is not a configuration file", firewall.ErrBadResponse)
	}
	return body, nil
}
