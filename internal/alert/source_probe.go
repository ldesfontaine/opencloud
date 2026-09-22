package alert

import (
	"context"
	"time"

	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Les motifs qui rendent un certificat non vérifié, par un mot.
const (
	ReasonChain    = "chain"
	ReasonHostname = "hostname"
	ReasonRevoked  = "revoked"
)

// Ce que le composant sonde constate : une sonde dont l'état vient de
// bouger, ou dont le certificat est à rejuger. Une sonde nouvelle ou en
// pause ne dit rien de la cible : tout ce qu'elle portait se résout.
func (e *Engine) ProbeChanged(ctx context.Context, found probe.Probe) {
	object := Object{Kind: ObjectProbe, ID: found.ID, Name: found.Name}
	if found.Status == probe.StatusPaused || found.Status == probe.StatusNew {
		e.Gone(ctx, object)
		return
	}
	if found.Status == probe.StatusDown {
		e.Open(ctx, Fact{
			Kind: KindProbeDown, Severity: SeverityDanger, Object: object, MachineID: found.MachineID,
			Details: Details{Target: found.Target, Reason: string(found.LastReason)},
		})
	} else {
		e.Resolve(ctx, KindProbeDown, object)
	}
	e.judgeCertificate(ctx, found, object)
}

func (e *Engine) ProbeRemoved(ctx context.Context, id string) {
	e.Gone(ctx, Object{Kind: ObjectProbe, ID: id})
}

// judgeCertificate lit le dernier certificat vu : l'échéance d'un côté,
// la confiance de l'autre, jamais fusionées. Une chaîne refusée parce que
// le certificat est expiré ne se compte pas deux fois.
func (e *Engine) judgeCertificate(ctx context.Context, found probe.Probe, object Object) {
	certificate := found.Certificate
	if certificate == nil {
		return
	}
	now := e.now()
	host := hostOf(found)
	details := Details{Target: host, NotAfter: certificate.NotAfter}
	expired := certificate.Expired(now)
	switch {
	case expired:
		e.Open(ctx, Fact{Kind: KindCertExpired, Severity: SeverityDanger, Object: object, MachineID: found.MachineID, Details: details})
		e.Resolve(ctx, KindCertExpiring, object)
	case certificate.NotAfter.Before(now.Add(probe.CertificateWarning * 24 * time.Hour)):
		severity := SeverityAttention
		if certificate.NotAfter.Before(now.Add(probe.CertificateDanger * 24 * time.Hour)) {
			severity = SeverityDanger
		}
		e.Open(ctx, Fact{Kind: KindCertExpiring, Severity: severity, Object: object, MachineID: found.MachineID, Details: details})
		e.Resolve(ctx, KindCertExpired, object)
	default:
		e.Resolve(ctx, KindCertExpiring, object)
		e.Resolve(ctx, KindCertExpired, object)
	}
	reason := untrustReason(certificate, expired)
	if reason == "" {
		e.Resolve(ctx, KindCertUntrusted, object)
		return
	}
	details.Reason = reason
	e.Open(ctx, Fact{Kind: KindCertUntrusted, Severity: SeverityAttention, Object: object, MachineID: found.MachineID, Details: details})
}

func untrustReason(certificate *probe.Certificate, expired bool) string {
	switch {
	case certificate.OCSP == probe.OCSPRevoked:
		return ReasonRevoked
	case !certificate.HostnameMatch:
		return ReasonHostname
	case !certificate.ChainValid && !expired:
		return ReasonChain
	}
	return ""
}

func hostOf(found probe.Probe) string {
	address, err := probe.ParseTarget(found.Kind, found.Target)
	if err != nil {
		return found.Target
	}
	return address.Host
}
