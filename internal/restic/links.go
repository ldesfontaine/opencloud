package restic

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// passwordBytes : 32 octets de crypto/rand, 43 caractères en base64url sans
// remplissage (agentwire.IsResticPassword).
const passwordBytes = 32

// secretLabel dit, dans l'audit de l'ordre, qu'un secret est parti.
const secretLabel = "passwords"

// Link est le lien d'une source vers une cible : un dépôt et ses deux mots
// de passe, tirés et scellés par le serveur. Le dernier résultat de
// rétention y vit aussi : la purge vise le dépôt entier.
type Link struct {
	ID              string
	SourceMachineID string
	TargetMachineID string
	// Vides quand le store liste les liens : ils ne s'ouvrent que pour un
	// ordre.
	Secrets   LinkSecrets
	CreatedAt time.Time
	// Zéro tant que le lien sert. Retiré, il n'a plus de sauvegarde, la cible
	// l'a fermé, mais ses mots de passe restent scellés : redéclaré, le même
	// dépôt se rouvre.
	RetiredAt time.Time
	Retain    RetainOutcome
}

// IsRetired dit si plus aucune sauvegarde n'emploie le lien.
func (l Link) IsRetired() bool {
	return !l.RetiredAt.IsZero()
}

// LinkSecrets sont les mots de passe d'un lien. Ils se masquent d'eux-mêmes
// dans un %v, un journal ou un JSON écrit par mégarde.
type LinkSecrets struct {
	REST string
	Repo string
}

const redactedSecrets = "[secret]"

func (LinkSecrets) String() string { return redactedSecrets }

func (LinkSecrets) LogValue() slog.Value { return slog.StringValue(redactedSecrets) }

func (LinkSecrets) MarshalJSON() ([]byte, error) { return json.Marshal(redactedSecrets) }

// isLost : la clé du serveur a été perdue (opencloud secrets forget) ; le
// lien n'a plus rien à envoyer.
func (s LinkSecrets) isLost() bool {
	return s.REST == "" || s.Repo == ""
}

// RetainOutcome est la dernière rétention d'un dépôt : l'ordre, son état et
// une raison fermée. State vide : jamais tentée.
type RetainOutcome struct {
	RunID  string
	State  string
	Reason string
	At     time.Time
}

func (l Link) repo() repoKey {
	return repoKey{source: l.SourceMachineID, target: l.TargetMachineID}
}

var (
	// ErrLinkNotFound : la base n'a pas ce lien (le store le rend).
	ErrLinkNotFound = errors.New("restic link not found")
	// ErrLinkSecretLost : la clé du serveur a été perdue, le lien n'a plus
	// ses mots de passe ; le dépôt reste, la source garde les siens.
	ErrLinkSecretLost = refusal.Conflict("restic.link_secret_lost", "the passwords of this link were lost with the server key: the source still backs up, the target no longer purges")
)

// drawSecrets tire les deux mots de passe d'un lien neuf.
func drawSecrets() LinkSecrets {
	return LinkSecrets{REST: drawPassword(), Repo: drawPassword()}
}

// drawPassword : crypto/rand.Read ne rend plus d'erreur depuis Go 1.24.
func drawPassword() string {
	raw := make([]byte, passwordBytes)
	_, _ = rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// D'où vient le lien qu'une déclaration ouvre : ce qu'il faut défaire si
// ses ordres ne partent pas.
type linkOrigin int

const (
	linkInService linkOrigin = iota
	linkDrawn
	linkReopened
)

// openedLink est le lien qu'une déclaration emploie, et son origine.
type openedLink struct {
	Link
	origin linkOrigin
}

// openLink rend le lien de la source vers la cible, avec ses mots de passe :
// celui qui sert, celui qui avait été retiré (remis en service, mêmes mots de
// passe, même dépôt), ou un neuf, tiré ici. Deux créations ensemble tombent
// d'accord : le store garde le premier.
func (s *Service) openLink(ctx context.Context, sourceID, targetID string) (openedLink, error) {
	id, err := ident.New()
	if err != nil {
		return openedLink{}, fmt.Errorf("new link id: %w", err)
	}
	candidate := Link{ID: id, SourceMachineID: sourceID, TargetMachineID: targetID, Secrets: drawSecrets(), CreatedAt: s.now()}
	link, isNew, err := s.store.EnsureResticLink(ctx, candidate)
	if err != nil {
		return openedLink{}, fmt.Errorf("ensure restic link: %w", err)
	}
	if link.Secrets.isLost() {
		return openedLink{}, ErrLinkSecretLost
	}
	opened := openedLink{Link: link, origin: linkInService}
	switch {
	case isNew:
		opened.origin = linkDrawn
	case link.IsRetired():
		if err := s.store.SetResticLinkRetired(ctx, link.ID, time.Time{}); err != nil {
			return openedLink{}, fmt.Errorf("reopen restic link: %w", err)
		}
		opened.origin, opened.RetiredAt = linkReopened, time.Time{}
		s.logger.Info("restic link reopened", "source", sourceID, "target", targetID)
	}
	return opened, nil
}

// provisionSource envoie à la source l'adresse, le certificat de la cible et
// les deux mots de passe du lien, dans le secret signé de l'ordre.
func (s *Service) provisionSource(ctx context.Context, link Link, target TargetRecord) error {
	targetUser, err := agentwire.ResticUserFor(target.MachineID)
	if err != nil {
		return ErrTargetUnknown
	}
	user, err := agentwire.ResticUserFor(link.SourceMachineID)
	if err != nil {
		return ErrSourceUnknown
	}
	params, err := json.Marshal(agentwire.ResticProvisionParams{
		Target: targetUser, Host: target.Reported.Host, Port: target.Reported.Port,
		User: user, Certificate: target.Reported.Certificate,
	})
	if err != nil {
		return fmt.Errorf("encode provision: %w", err)
	}
	secret := agentwire.ResticProvisionSecret{RESTPassword: link.Secrets.REST, RepoPassword: link.Secrets.Repo}
	_, err = s.sendSecret(ctx, link.SourceMachineID, agentwire.KindResticProvision, params, secret, ErrSourceRightMissing)
	return err
}

// openUser ouvre le dépôt de la source sur la cible : son utilisateur et le
// mot de passe REST. Le mot de passe du dépôt ne part pas : la cible ne le
// garde pas.
func (s *Service) openUser(ctx context.Context, link Link) error {
	user, err := agentwire.ResticUserFor(link.SourceMachineID)
	if err != nil {
		return ErrSourceUnknown
	}
	params, err := json.Marshal(agentwire.ResticUserAddParams{User: user})
	if err != nil {
		return fmt.Errorf("encode user add: %w", err)
	}
	secret := agentwire.ResticUserSecret{RESTPassword: link.Secrets.REST}
	_, err = s.sendSecret(ctx, link.TargetMachineID, agentwire.KindResticUserAdd, params, secret, ErrTargetRightMissing)
	return err
}

// closeUser ferme le dépôt d'un utilisateur sur la cible, sans secret : la
// ligne disparaît, les données restent.
func (s *Service) closeUser(ctx context.Context, targetID, user string) error {
	params, err := json.Marshal(agentwire.ResticUserRemoveParams{User: user})
	if err != nil {
		return fmt.Errorf("encode user remove: %w", err)
	}
	_, err = s.send(ctx, targetID, agentwire.KindResticUserRemove, params, ErrTargetRightMissing)
	return err
}

// undoOpen défait ce qu'openLink a fait quand les ordres du lien ne sont
// pas partis : un lien neuf s'efface (rien n'a été écrit avec ses mots de
// passe), un lien rouvert est retiré de nouveau.
func (s *Service) undoOpen(ctx context.Context, opened openedLink) {
	var err error
	switch opened.origin {
	case linkDrawn:
		err = s.store.DeleteResticLink(ctx, opened.SourceMachineID, opened.TargetMachineID)
	case linkReopened:
		err = s.store.SetResticLinkRetired(ctx, opened.ID, s.now())
	}
	if err != nil {
		s.logger.Error("undo restic link", "source", opened.SourceMachineID, "target", opened.TargetMachineID, "error", err)
	}
}

// closeLinkIfUnused ferme le dépôt quand plus aucune sauvegarde de la source
// n'y va : restic.user.remove à la cible, puis le lien est retiré. Ses mots
// de passe restent scellés : le dépôt, toujours sur la cible, ne s'ouvre
// qu'avec eux si le couple est redéclaré. Un ordre qui ne part pas n'empêche
// pas le retrait : le prochain rapport de la cible rejouera la fermeture
// (healTarget).
func (s *Service) closeLinkIfUnused(ctx context.Context, removed Backup) {
	remaining, err := s.store.ListResticBackups(ctx)
	if err != nil {
		s.logger.Warn("list restic backups", "error", err)
		return
	}
	for _, other := range remaining {
		if other.repo() == removed.repo() {
			return
		}
	}
	if err := s.closeUser(ctx, removed.TargetMachineID, removed.RepoUser); err != nil {
		s.logger.Warn("restic user not removed from the target", "target", removed.TargetMachineID, "error", err)
	}
	link, err := s.store.GetResticLink(ctx, removed.SourceMachineID, removed.TargetMachineID)
	if err == nil {
		err = s.store.SetResticLinkRetired(ctx, link.ID, s.now())
	}
	if err != nil && !errors.Is(err, ErrLinkNotFound) {
		s.logger.Error("retire restic link", "source", removed.SourceMachineID, "target", removed.TargetMachineID, "error", err)
	}
}

// healTarget rejoue ce que la cible a perdu ou changé, d'après son rapport :
// un certificat, une adresse ou un port neufs repartent à chaque source liée
// (restic.provision, mêmes mots de passe) ; un utilisateur lié absent du
// rapport est rouvert (restic.user.add) ; un utilisateur sans lien est fermé.
func (s *Service) healTarget(ctx context.Context, previous, current TargetRecord) {
	links, err := s.linksOf(ctx, func(link Link) bool { return link.TargetMachineID == current.MachineID })
	if err != nil {
		s.logger.Warn("list restic links", "error", err)
		return
	}
	hasMoved := previous.Reported.Fingerprint != current.Reported.Fingerprint ||
		previous.Reported.Host != current.Reported.Host || previous.Reported.Port != current.Reported.Port
	linkedUsers := []string{}
	for _, link := range links {
		user, err := agentwire.ResticUserFor(link.SourceMachineID)
		if err != nil {
			continue
		}
		linkedUsers = append(linkedUsers, user)
		if hasMoved && current.IsReady() {
			s.resend(ctx, link, func(opened Link) error { return s.provisionSource(ctx, opened, current) })
		}
		if !slices.Contains(current.Reported.Users, user) {
			s.resend(ctx, link, func(opened Link) error { return s.openUser(ctx, opened) })
		}
	}
	for _, user := range current.Reported.Users {
		if slices.Contains(linkedUsers, user) {
			continue
		}
		if err := s.closeUser(ctx, current.MachineID, user); err != nil {
			s.logger.Warn("restic user without link not removed", "target", current.MachineID, "error", err)
		}
	}
}

// healSource renvoie restic.provision pour chaque lien de la source que son
// rapport ne cite pas : un dossier d'état perdu, un agent réinstallé.
func (s *Service) healSource(ctx context.Context, sourceID string, report agentwire.ResticSourceReport) {
	links, err := s.linksOf(ctx, func(link Link) bool { return link.SourceMachineID == sourceID })
	if err != nil {
		s.logger.Warn("list restic links", "error", err)
		return
	}
	for _, link := range links {
		targetUser, err := agentwire.ResticUserFor(link.TargetMachineID)
		if err != nil || slices.Contains(report.Targets, targetUser) {
			continue
		}
		target, err := s.store.GetResticTarget(ctx, link.TargetMachineID)
		if err != nil || !target.IsReady() {
			continue
		}
		s.resend(ctx, link, func(opened Link) error { return s.provisionSource(ctx, opened, target) })
	}
}

func (s *Service) linksOf(ctx context.Context, match func(Link) bool) ([]Link, error) {
	links, err := s.store.ListResticLinks(ctx)
	if err != nil {
		return nil, err
	}
	matching := []Link{}
	for _, link := range links {
		if match(link) {
			matching = append(matching, link)
		}
	}
	return matching, nil
}

// resend ouvre les mots de passe d'un lien le temps d'un ordre. Un échec se
// journalise sans secret ; le prochain rapport le rejouera.
func (s *Service) resend(ctx context.Context, listed Link, send func(Link) error) {
	link, err := s.store.GetResticLink(ctx, listed.SourceMachineID, listed.TargetMachineID)
	if err == nil && link.Secrets.isLost() {
		err = ErrLinkSecretLost
	}
	if err == nil {
		err = send(link)
	}
	if err != nil {
		s.logger.Warn("restic link not replayed", "source", listed.SourceMachineID, "target", listed.TargetMachineID, "error", err)
		return
	}
	s.logger.Info("restic link replayed", "source", listed.SourceMachineID, "target", listed.TargetMachineID)
}

// ruleContext : ce qui part d'un rapport vers une autre machine est une
// règle du serveur, pas l'agent qui a rapporté.
func ruleContext(ctx context.Context) context.Context {
	return audit.WithActor(ctx, audit.Rule("restic"))
}
