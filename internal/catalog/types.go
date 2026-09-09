package catalog

import (
	"io/fs"
	"time"
)

// Kind est le nom d'une action, en minuscules : c'est aussi le nom du dossier
// de son script sous internal/scripts.
type Kind string

const (
	KindDiagnostiquer Kind = "diagnostiquer"
	KindEnroler       Kind = "enroler"
)

// Scope : ce que l'action touche (05-execution.md, « les quatre attributs »).
type Scope string

const (
	ScopeInfrastructure Scope = "infrastructure"
	ScopeMachine        Scope = "machine"
	ScopeEnvironment    Scope = "environment"
	ScopeService        Scope = "service"
	ScopeDomain         Scope = "domain"
)

// Place : où l'action s'exécute.
type Place string

const (
	PlaceTarget     Place = "target"      // sur la machine cible
	PlaceOpenCloud  Place = "opencloud"   // sur la machine openCloud
	PlaceThirdParty Place = "third-party" // chez un tiers (DNS)
)

// ParamType nomme la validation d'un paramètre (15-catalogue-actions.md §4).
// Le type dit la forme ; le package validate la vérifie.
type ParamType string

const (
	ParamDomain    ParamType = "domain"
	ParamPort      ParamType = "port"
	ParamSlug      ParamType = "slug" // nom de service ou d'environnement
	ParamAccount   ParamType = "account"
	ParamSlot      ParamType = "slot"
	ParamDigest    ParamType = "digest"
	ParamAddress   ParamType = "address"
	ParamPublicKey ParamType = "public-key"
)

// ParamSpec décrit un paramètre d'action. Name devient OC_<NAME> dans
// params.env, en majuscules.
type ParamSpec struct {
	Name     string
	Label    string
	Type     ParamType
	Required bool
}

// Definition est ce que l'interface montre avant de lancer, et ce que le
// runner applique : le délai devient RuntimeMaxSec sur la machine.
type Definition struct {
	Kind       Kind
	Label      string // « Diagnostiquer »
	Summary    string // une phrase pour l'écran « avant »
	Scope      Scope
	Place      Place
	Reversible bool
	Interrupts bool
	Timeout    time.Duration
	Params     []ParamSpec
}

// NeedsConfirmation : une action irréversible ou interruptrice se confirme,
// les autres non (05-execution.md).
func (d Definition) NeedsConfirmation() bool {
	return !d.Reversible || d.Interrupts
}

// File est un fichier rendu par Go, relatif à files/ dans le dossier de
// l'action. Le script le pose, il ne le compose pas.
type File struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
}

// Prepared est tout ce qu'une action dépose. Aucune valeur saisie n'apparaît
// ailleurs que dans ParamsEnv et Files : jamais dans une ligne de commande.
type Prepared struct {
	Definition   Definition
	Params       map[string]string // validés, par nom de ParamSpec
	Script       []byte            // run.sh complet, en-tête commun inclus, tel que déposé
	ScriptDigest string            // « sha256:<hex> » de Script
	ParamsEnv    []byte            // rendu, quoting repris de your-cloud (% doublé, guillemets si espace)
	Files        []File
}

// Ce que tout script écrit et rend (15-catalogue-actions.md §1) : une ligne
// par étape, une ligne de constat, puis le code.
const (
	StepPrefix   = "étape:"
	InfoPrefix   = "info:"
	ResultPrefix = "résultat:"

	ExitDone    = 0 // fait, ou « inchangé »
	ExitFailed  = 1 // échoué
	ExitRefused = 2 // refusé, nommément, avant tout effet
)
