package probe

import (
	"slices"
	"time"
	"unicode/utf8"
)

// validate refuse en bloc un rapport hors de ce qu'un agent peut avoir
// sondé : trop d'essais, une date absurde, une durée négative, un code
// qui n'est pas un code. Le signal de vie compte quand même : c'est le
// serveur qui répond, pas ce paquet.
func validate(report Report, now time.Time) error {
	if len(report.Results) > MaxResultsPerReport {
		return ErrReportInvalid
	}
	for _, result := range report.Results {
		if err := validateResult(result, now); err != nil {
			return err
		}
	}
	return nil
}

func validateResult(result Result, now time.Time) error {
	if result.ProbeID == "" || len(result.ProbeID) > 64 {
		return ErrReportInvalid
	}
	if result.CheckedAt.IsZero() || result.CheckedAt.After(now.Add(maxFutureSkew)) || result.CheckedAt.Before(now.Add(-ResultRetention)) {
		return ErrReportInvalid
	}
	if !slices.Contains(Outcomes(), result.Outcome) {
		return ErrReportInvalid
	}
	if result.DurationMs < 0 || result.DurationMs > maxDurationMs {
		return ErrReportInvalid
	}
	if result.Code != nil && (*result.Code < 100 || *result.Code > 599) {
		return ErrReportInvalid
	}
	if !isReason(result.Reason) {
		return ErrReportInvalid
	}
	return validateCertificate(result.Certificate)
}

func validateCertificate(certificate *Certificate) error {
	if certificate == nil {
		return nil
	}
	if utf8.RuneCountInString(certificate.Subject) > MaxIssuerLength || utf8.RuneCountInString(certificate.Issuer) > MaxIssuerLength {
		return ErrReportInvalid
	}
	if len(certificate.Fingerprint) > 64 {
		return ErrReportInvalid
	}
	if !isOCSP(certificate.OCSP) {
		return ErrReportInvalid
	}
	// Un certificat que la sonde a vu porte forcément ses deux dates ; leur
	// contenu, lui, est le fait de la cible, pas de l'agent.
	if certificate.NotBefore.IsZero() || certificate.NotAfter.IsZero() {
		return ErrReportInvalid
	}
	return nil
}
