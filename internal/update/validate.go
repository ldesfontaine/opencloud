package update

import (
	"regexp"
	"time"
)

const maxFutureSkew = 5 * time.Minute

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// validate refuse en bloc un rapport hors de ce qu'un agent peut avoir
// constaté : trop de résultats, une image sans nom, une empreinte qui
// n'en est pas une, une date dans le futur.
func validate(report Report, now time.Time) error {
	if len(report.Results) > MaxResultsPerReport {
		return ErrReportInvalid
	}
	for _, result := range report.Results {
		if result.Image == "" || len(result.Image) > MaxImageLength || !isOutcome(result.Outcome) {
			return ErrReportInvalid
		}
		if result.CheckedAt.IsZero() || result.CheckedAt.After(now.Add(maxFutureSkew)) {
			return ErrReportInvalid
		}
		if !isDigest(result.LocalDigest) || !isDigest(result.RemoteDigest) || !isDigest(result.NewerDigest) {
			return ErrReportInvalid
		}
		if len(result.NewerTag) > MaxTagLength {
			return ErrReportInvalid
		}
	}
	return nil
}

func isDigest(digest string) bool {
	return digest == "" || digestPattern.MatchString(digest)
}
