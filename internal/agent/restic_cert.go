package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/fsx"
)

// Le certificat de rest-server : auto-signé, ECDSA P-256, un an. Les sources
// l'épinglent (RESTIC_CACERT) ; il se tire à neuf quand les adresses
// annoncées changent ou qu'il expire dans moins de certificateRenewBefore.
// La clé privée ne quitte jamais l'état de l'agent.
const (
	certificateLifetime    = 365 * 24 * time.Hour
	certificateRenewBefore = 30 * 24 * time.Hour
	certificateSerialBits  = 128
	targetCertificateFile  = "target.crt"
	targetKeyFile          = "target.key"
	maxCertificateFile     = 16 << 10
)

// ensureTargetCertificate garde le certificat en place s'il convient encore,
// sinon en tire un neuf. Il rend le PEM public.
func ensureTargetCertificate(dir *os.Root, hosts []string, now time.Time) (string, error) {
	if current, ok := reusableCertificate(dir, hosts, now); ok {
		return current, nil
	}
	certificatePEM, keyPEM, err := newTargetCertificate(hosts, now)
	if err != nil {
		return "", err
	}
	// La clé d'abord : un certificat neuf sans sa clé ne se lancerait pas.
	if err := fsx.WriteAtomic(dir, targetKeyFile, keyPEM, resticFileMode); err != nil {
		return "", fmt.Errorf("write target key: %w", err)
	}
	if err := fsx.WriteAtomic(dir, targetCertificateFile, certificatePEM, resticFileMode); err != nil {
		return "", fmt.Errorf("write target certificate: %w", err)
	}
	return string(certificatePEM), nil
}

// reusableCertificate : la paire se lit, le certificat porte exactement ces
// adresses et ne va pas expirer.
func reusableCertificate(dir *os.Root, hosts []string, now time.Time) (string, bool) {
	certificatePEM, err := fsx.ReadPrivate(dir, targetCertificateFile, maxCertificateFile)
	if err != nil {
		return "", false
	}
	keyPEM, err := fsx.ReadPrivate(dir, targetKeyFile, maxCertificateFile)
	if err != nil {
		return "", false
	}
	pair, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil || len(pair.Certificate) != 1 {
		return "", false
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || certificate.NotAfter.Sub(now) < certificateRenewBefore {
		return "", false
	}
	if !slices.Equal(certificateHosts(certificate), sortedHosts(hosts)) {
		return "", false
	}
	return string(certificatePEM), true
}

// newTargetCertificate tire la clé et le certificat ; les adresses IP vont
// dans IPAddresses, les noms dans DNSNames : restic vérifie l'hôte de son URL.
func newTargetCertificate(hosts []string, now time.Time) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate target key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), certificateSerialBits))
	if err != nil {
		return nil, nil, fmt.Errorf("draw certificate serial: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(certificateLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create target certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("encode target key: %w", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certificatePEM, keyPEM, nil
}

// certificateHosts rend les adresses d'un certificat, triées, comme
// sortedHosts écrit celles demandées.
func certificateHosts(certificate *x509.Certificate) []string {
	hosts := slices.Clone(certificate.DNSNames)
	for _, ip := range certificate.IPAddresses {
		hosts = append(hosts, ip.String())
	}
	slices.Sort(hosts)
	return hosts
}

func sortedHosts(hosts []string) []string {
	sorted := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			host = ip.String()
		}
		sorted = append(sorted, host)
	}
	slices.Sort(sorted)
	return slices.Compact(sorted)
}
