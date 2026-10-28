package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

// Le fichier du jeton tient en trois lignes de JSON : au-delà, ce n'est pas
// lui.
const maxProxmoxTokenBytes = 4 << 10

// proxmoxToken est le jeton local de l'agent : l'opérateur l'a posé dans l'état
// de l'agent (machine.ProxmoxTokenCommands). Le secret n'en sort jamais :
// ni journal, ni résultat d'ordre, ni message d'erreur.
type proxmoxToken struct {
	ID          string `json:"token_id"`
	Secret      string `json:"secret"` // #nosec G117 -- le champ existe pour porter le secret du jeton, lu du fichier de l'agent.
	Fingerprint string `json:"fingerprint"`
}

// readProxmoxToken lit et juge le fichier du jeton. Il rend le jeton, ou la
// raison pour laquelle l'agent ne s'en sert pas : absent ou illisible
// (proxmox_token_missing), lisible d'autres que le compte de l'agent ou d'un
// autre propriétaire (proxmox_token_unsafe), sans empreinte du certificat
// (proxmox_pin_missing). Le fichier est ouvert par l'état de l'agent (os.Root) :
// un lien ne le fait pas sortir de l'état.
func readProxmoxToken(stateDir *os.Root) (proxmoxToken, string) {
	file, err := stateDir.Open(brand.AgentProxmoxFile)
	if err != nil {
		return proxmoxToken{}, agentwire.ReasonProxmoxTokenMissing
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return proxmoxToken{}, agentwire.ReasonProxmoxTokenMissing
	}
	if !isPrivateToCaller(info) {
		return proxmoxToken{}, agentwire.ReasonProxmoxTokenUnsafe
	}
	content, err := io.ReadAll(io.LimitReader(file, maxProxmoxTokenBytes+1))
	if err != nil || len(content) > maxProxmoxTokenBytes {
		return proxmoxToken{}, agentwire.ReasonProxmoxTokenMissing
	}
	var token proxmoxToken
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&token); err != nil {
		return proxmoxToken{}, agentwire.ReasonProxmoxTokenMissing
	}
	return judgeProxmoxToken(token)
}

// isPrivateToCaller : aucun droit pour le groupe ni pour les autres, et le
// fichier est à celui qui l'ouvre (le compte de l'agent). Un jeton lisible d'un
// autre compte n'est plus un secret.
func isPrivateToCaller(info os.FileInfo) bool {
	if info.Mode().Perm()&0o077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Geteuid())
}

// judgeProxmoxToken refuse un jeton mal formé, ou sans empreinte, avant de
// s'en servir. La forme est celle que le serveur exige d'un jeton déclaré.
func judgeProxmoxToken(token proxmoxToken) (proxmoxToken, string) {
	normalized, err := proxmox.Definition{
		APIURL: machine.ProxmoxLocalAPI, TokenID: token.ID, TokenSecret: token.Secret, Pin: token.Fingerprint,
	}.Normalize()
	switch {
	case errors.Is(err, proxmox.ErrPinInvalid):
		return proxmoxToken{}, agentwire.ReasonProxmoxPinMissing
	case err != nil:
		return proxmoxToken{}, agentwire.ReasonProxmoxTokenMissing
	case normalized.Pin == "":
		return proxmoxToken{}, agentwire.ReasonProxmoxPinMissing
	}
	return proxmoxToken{ID: normalized.TokenID, Secret: normalized.TokenSecret, Fingerprint: normalized.Pin}, ""
}
