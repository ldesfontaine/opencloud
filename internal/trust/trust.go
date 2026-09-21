package trust

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// Load rend le magasin du système, augmenté des autorités du paquet PEM
// donné. Un chemin vide rend nil, que tls.Config.RootCAs comme
// x509.VerifyOptions.Roots lisent « prends le magasin du système » : une
// installation sans autorité interne garde exactement le comportement
// qu'elle avait sans ce paquet.
//
// Rien n'est avalé. C'est le piège de SSL_CERT_FILE, qui remplace le
// magasin au lieu de l'étendre, et dont le fichier illisible rend un jeu
// vide sans erreur : toutes les chaînes deviennent « autorité inconnue »
// sans une ligne de journal. Ici, un chemin faux arrête le démarrage.
func Load(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	bundle, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ca file %s: %w", path, err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system ca pool: %w", err)
	}
	if !roots.AppendCertsFromPEM(bundle) {
		return nil, fmt.Errorf("ca file %s holds no certificate", path)
	}
	return roots, nil
}

// Le chemin vient de l'opérateur ; il est lu via os.Root, borné à son
// propre dossier, comme tout accès fichier du projet.
func readFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Base(path))
}
