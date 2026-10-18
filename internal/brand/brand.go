package brand

// A. Affichage.

// ProductName est le nom que voit l'opérateur. Renommer le produit
// n'abîme rien d'installé : seuls les textes changent. Les élisions
// françaises des catalogues (« d'{product} ») supposent un nom qui commence
// par une voyelle ; un nom à consonne initiale demande de les relire.
const ProductName = "openCloud"

// B. Identifiant technique.

// ID est la forme technique du nom : chemins, fichiers, noms d'unités. Le
// changer déplace tous les chemins ci-dessous ; une installation existante
// devra migrer son fichier de configuration, sa base et l'état de ses
// agents.
const ID = "opencloud"

const (
	ConfigDir     = "/etc/" + ID
	ConfigPath    = ConfigDir + "/config.toml"
	StateDir      = "/var/lib/" + ID
	AgentStateDir = StateDir + "/agent"
	DatabaseFile  = ID + ".db"

	// Le nom de la machine qui héberge le serveur, dans la base.
	LocalMachineName = ID
	// Le nom sous lequel un client IA enregistre le serveur MCP.
	MCPServerKey = ID
	// Préfixe des identifiants de transaction envoyés à Matrix.
	MatrixTransactionPrefix = ID + "-"
)

// Réservées : pas encore lues, posées pour que le nom n'ait pas à être
// réécrit le jour où elles servent.
const (
	LabelPrefix     = ID + "."
	WorkspaceMarker = "." + ID
	ServerUnit      = ID + ".service"
	AgentUnit       = ID + "-agent.service"
)

// Command est le nom du binaire. Littéral et non dérivé de ID : il suit le
// répertoire cmd/opencloud et le Makefile, qu'un changement de ID ne
// renomme pas.
const Command = "opencloud"

// Les User-Agent envoyés aux services externes. Valeurs d'aujourd'hui,
// gardées telles quelles : un tiers peut s'en servir pour filtrer.
const (
	UserAgent          = ProductName
	RegistryUserAgent  = ProductName + "-agent"
	LifecycleUserAgent = ID
)

// C. Empreinte du protocole.
//
// Littéraux figés, qui ne dérivent ni de ProductName ni de ID. Les changer
// casse les agents installés, les récepteurs de webhooks et les jetons déjà
// émis : il faudrait d'abord une transition qui accepte les deux formes.
const (
	HeaderPrefix           = "OpenCloud-"
	WebhookEventHeader     = HeaderPrefix + "Event"
	WebhookSignatureHeader = HeaderPrefix + "Signature"
)

// Les préfixes de jetons : reconnaissables dans un journal ou un
// presse-papier, jamais confondus entre eux (le jeton de ping, hb_, n'est
// pas une marque et reste dans internal/heartbeat).
const (
	TokenPrefix           = "oc"
	EnrollmentTokenScheme = TokenPrefix + "_"  // #nosec G101 -- un préfixe, pas un secret.
	MCPSecretScheme       = TokenPrefix + "s_" // #nosec G101
	MCPCodeScheme         = TokenPrefix + "c_" // #nosec G101
	MCPAccessScheme       = TokenPrefix + "a_" // #nosec G101
	MCPRefreshScheme      = TokenPrefix + "r_" // #nosec G101
	MCPAPIScheme          = TokenPrefix + "k_" // #nosec G101
)

const (
	// Début de l'empreinte de l'identifiant d'une machine, pour reconnaître
	// un agent installé sur la machine du serveur.
	HostFingerprintPrefix = "opencloud-host:"
	// Le seul client OAuth du serveur MCP.
	OAuthClientID = "opencloud"
	// Le code d'erreur qu'un agent reçoit quand il tourne sur la machine du
	// serveur ; l'agent s'en sert pour choisir son message.
	AgentErrorIsHost = "machine_is_opencloud"
)
