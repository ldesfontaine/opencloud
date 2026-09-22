package update

import (
	"errors"
	"time"

	"github.com/ldesfontaine/opencloud/internal/registry"
)

var (
	ErrReportInvalid  = errors.New("image report out of range")
	ErrMachineOffline = errors.New("machine offline")
	ErrBadPolicy      = errors.New("unknown update policy")
)

// Check est le dernier constat gardé par (machine, image) : les faits de
// l'agent, et ce que le serveur en déduit.
type Check struct {
	MachineID    string
	Image        string
	CheckedAt    time.Time
	Outcome      Outcome
	LocalDigest  string
	RemoteDigest string
	NewerTag     string
	NewerDigest  string
	Kind         Kind
}

// HasUpdate dit si l'image a quelque chose de plus récent à offrir.
func (c Check) HasUpdate() bool {
	return c.Kind != ""
}

// Target rend l'image à tirer : le tag plus récent, ou le même tag dont
// l'empreinte a bougé.
func (c Check) Target() string {
	ref, err := registry.Parse(c.Image)
	if err != nil {
		return c.Image
	}
	if c.NewerTag != "" {
		return ref.WithTag(c.NewerTag).String()
	}
	return ref.String()
}

// kindOf déduit le type : un tag plus récent dit ce qu'il change ; sinon
// une empreinte distante différente de celle tirée dit que le même tag a
// été reconstruit.
func kindOf(result Result) Kind {
	if result.Outcome != OutcomeOK {
		return ""
	}
	ref, err := registry.Parse(result.Image)
	if err != nil {
		return ""
	}
	if result.NewerTag != "" {
		if kind := Classify(ref.Tag, result.NewerTag); kind != "" {
			return kind
		}
	}
	if result.RemoteDigest != "" && result.LocalDigest != "" && result.RemoteDigest != result.LocalDigest {
		return KindDigest
	}
	return ""
}

// checkOf fait un constat d'un résultat ; un tag plus récent qui ne se
// compare pas au courant est effacé : il ne sert à rien.
func checkOf(machineID string, result Result) Check {
	check := Check{
		MachineID: machineID, Image: result.Image, CheckedAt: result.CheckedAt.UTC(), Outcome: result.Outcome,
		LocalDigest: result.LocalDigest, RemoteDigest: result.RemoteDigest, NewerTag: result.NewerTag, NewerDigest: result.NewerDigest,
	}
	check.Kind = kindOf(result)
	if check.Kind == "" || check.Kind == KindDigest {
		check.NewerTag, check.NewerDigest = "", ""
	}
	return check
}
