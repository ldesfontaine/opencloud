package machine

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// La machine openCloud est une machine comme les autres, avec un agent comme
// les autres. Deux choses seulement la touchent ici : l'agent qui s'enrôle
// depuis la machine du serveur prend son identifiant stable, LocalID ; et
// tant qu'elle n'a jamais eu d'agent, le serveur offre la ligne qui
// l'installe, dès la première configuration.

// serverHost est la machine où tourne serve, lue au démarrage.
type serverHost struct {
	fingerprint string
	hostname    string
}

// SetServerHost retient l'empreinte et le nom d'hôte de la machine du
// serveur (hostinfo). Sans empreinte, aucun agent n'est reconnu comme le
// sien.
func (s *Service) SetServerHost(fingerprint, hostname string) {
	s.server = serverHost{fingerprint: fingerprint, hostname: hostname}
}

// machineIDFor rend l'identifiant sous lequel l'agent s'enrôle : LocalID
// s'il présente l'empreinte de la machine du serveur (c'est ainsi qu'on
// reconnaît la machine openCloud, et qu'elle retrouve son historique),
// sinon celui qu'il a tiré. L'empreinte est un SHA-256 de /etc/machine-id et
// du boot_id, que seule cette machine lit ; elle ne prouve rien de plus, et
// ne sert qu'à qui tient déjà un jeton, donc le droit d'enrôler.
func (s *Service) machineIDFor(request Enrollment) string {
	if s.server.fingerprint != "" && request.HostFingerprint == s.server.fingerprint {
		return LocalID
	}
	return request.MachineID
}

// localOffer est la ligne offerte pour la machine openCloud : le clair du
// jeton ne vit qu'en mémoire, jamais en base ni au journal.
type localOffer struct {
	cleartext string
	token     Token
}

// OfferLocalEnrollment rend le jeton de l'agent de la machine openCloud, tant
// qu'elle n'a jamais eu d'agent : celui déjà offert s'il attend encore, sinon
// un neuf, avec observe seul. Un jeton de ré-enrôlement si sa ligne existe
// (une base d'avant les agents), un jeton de machine neuve, nommée d'après
// le nom d'hôte, sinon : l'empreinte la rattache à LocalID. ErrLocalEnrolled
// quand elle a, ou a eu, un agent : un agent révoqué ne se réinstalle pas de
// lui-même.
func (s *Service) OfferLocalEnrollment(ctx context.Context) (string, Token, error) {
	s.offerLock.Lock()
	defer s.offerLock.Unlock()
	existing, err := s.store.GetMachine(ctx, LocalID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", Token{}, err
	}
	hasRow := err == nil
	if hasRow && existing.Agent.Exists() {
		return "", Token{}, ErrLocalEnrolled
	}
	if live, err := s.isPending(ctx, s.offer.token); err != nil || live {
		return s.offer.cleartext, s.offer.token, err
	}
	observeOnly := []Capability{{Name: CapabilityObserve}}
	name, machineID := s.localMachineName(), ""
	if hasRow {
		name, machineID = existing.Name, LocalID
	}
	cleartext, token, err := s.issueToken(ctx, name, machineID, observeOnly)
	if err != nil {
		return "", Token{}, err
	}
	s.offer = localOffer{cleartext: cleartext, token: token}
	return cleartext, token, nil
}

// isPending dit si le jeton attend encore son agent : ni servi, ni annulé,
// ni expiré.
func (s *Service) isPending(ctx context.Context, token Token) (bool, error) {
	if token.ID == "" {
		return false, nil
	}
	pending, err := s.store.ListPendingTokens(ctx, s.now())
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(pending, func(candidate Token) bool { return candidate.ID == token.ID }), nil
}

// localMachineName tire le nom de la machine openCloud de son nom d'hôte,
// sans le domaine ; un nom d'hôte qui ne fait pas un nom de machine laisse
// celui du produit.
func (s *Service) localMachineName() string {
	label, _, _ := strings.Cut(strings.ToLower(s.server.hostname), ".")
	if ValidateName(label) != nil {
		return brand.LocalMachineName
	}
	return label
}
