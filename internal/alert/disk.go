package alert

import "github.com/ldesfontaine/opencloud/internal/refusal"

const (
	// Les bornes d'un seuil réglé : sous 50 %, un volume ordinaire alerte
	// en permanence ; à 100 %, il est déjà trop tard.
	MinDiskAttentionPercent = 50
	MaxDiskDangerPercent    = 99
	// Le retour se tient cinq points sous l'attention : un volume qui oscille
	// autour du seuil ne fait pas clignoter l'alerte.
	DiskRecoveryGap = 5
)

// La clé est celle du réglage, que l'opérateur corrige dans les paramètres.
var ErrDiskThresholdsInvalid = refusal.Invalid("settings.disk_invalid", "alert: disk thresholds invalid")

// DiskThresholds : un volume alerte à Attention, s'aggrave à Danger, et ne
// se résout que sous Recovery.
type DiskThresholds struct {
	Attention int
	Danger    int
}

func DefaultDiskThresholds() DiskThresholds {
	return DiskThresholds{Attention: DefaultDiskAttentionPercent, Danger: DefaultDiskDangerPercent}
}

// DiskThresholdsOr rend les seuils réglés, ou ceux par défaut quand rien
// n'est réglé ou que la base porte une valeur hors bornes : une valeur
// écrite à la main ne coupe jamais l'alerte.
func DiskThresholdsOr(attention, danger int) DiskThresholds {
	thresholds := DiskThresholds{Attention: attention, Danger: danger}
	if thresholds.Validate() != nil {
		return DefaultDiskThresholds()
	}
	return thresholds
}

func (t DiskThresholds) Recovery() int {
	return t.Attention - DiskRecoveryGap
}

func (t DiskThresholds) Validate() error {
	if t.Attention < MinDiskAttentionPercent || t.Attention >= t.Danger || t.Danger > MaxDiskDangerPercent {
		return ErrDiskThresholdsInvalid
	}
	return nil
}
