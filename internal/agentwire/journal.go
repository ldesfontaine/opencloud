package agentwire

import (
	"encoding/json"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// Ce que le journal local de l'agent (journald) retient des paramètres d'un
// ordre : une liste blanche de champs par genre, déclarée ici avec le genre.
// Jamais le secret de l'ordre (OrderBody.Secret), jamais un champ qui porte un
// contenu (compose, certificat, clés) : un champ absent de la liste ne se
// journalise pas. Un test exige une entrée, même vide, pour chaque genre.
var journalFields = map[string][]string{
	"agent.ping":                 {},
	"agent.update":               {"version", "parcel"},
	"agent.rollback":             {},
	"agent.server_key":           {"public_key"},
	"backup.receive":             {"parcel", "path"},
	"backup.prune":               {"path", "prefix", "keep"},
	"backup.produce":             {"producer", "target", "prefix", "recipient", "parcel"},
	"docker.deploy":              {"service_id", "project"},
	"docker.update":              {"service_id", "project"},
	"docker.rollback":            {"project"},
	"proxmox.vm.start":           {"vmid", "type"},
	"proxmox.vm.shutdown":        {"vmid", "type"},
	"proxmox.vm.snapshot":        {"vmid", "type", "snapshot"},
	"proxmox.vm.snapshot_delete": {"vmid", "type", "snapshot"},
	"restic.target.setup":        {"listen", "port"},
	"restic.user.add":            {"user"},
	"restic.user.remove":         {"user"},
	"restic.provision":           {"target", "host", "port", "user"},
	"restic.backup":              {"target", "scope", "vmid", "guest_type", "service", "force_relay"},
	"restic.retain":              {"user", "keep_last", "keep_daily", "keep_weekly", "keep_monthly", "keep_yearly"},
	"restic.verify":              {"user"},
	"restic.browse":              {"user", "snapshot", "path"},
	"restic.restore":             {"target", "snapshot", "path", "stamp"},
}

// Une valeur journalisée tient en une ligne courte : au-delà, ce n'est plus un
// identifiant mais un contenu.
const maxJournalValueRunes = 200

// JournalFields rend les champs que le journal retient pour ce genre ; ok est
// faux pour un genre sans entrée, dont rien ne se journalise.
func JournalFields(kind Kind) ([]string, bool) {
	fields, ok := journalFields[string(kind)]
	return fields, ok
}

// JournalParams rend, des paramètres d'un ordre, les seuls champs de la liste
// blanche de son genre, en texte : nombres, booléens et chaînes imprimables et
// courtes. Tout autre champ, et tout autre type de valeur, est laissé.
func JournalParams(kind Kind, params json.RawMessage) map[string]string {
	fields, _ := JournalFields(kind)
	var all map[string]json.RawMessage
	if len(fields) == 0 || json.Unmarshal(params, &all) != nil {
		return nil
	}
	kept := map[string]string{}
	for _, field := range fields {
		if text, ok := journalText(all[field]); ok {
			kept[field] = text
		}
	}
	return kept
}

func journalText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, isJournalText(text)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil && number != "" {
		return number.String(), true
	}
	var flag bool
	if json.Unmarshal(raw, &flag) == nil {
		return strconv.FormatBool(flag), true
	}
	return "", false
}

func isJournalText(text string) bool {
	if utf8.RuneCountInString(text) > maxJournalValueRunes {
		return false
	}
	for _, r := range text {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
