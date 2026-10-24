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
	ConfigDir  = "/etc/" + ID
	ConfigPath = ConfigDir + "/config.toml"
	StateDir   = "/var/lib/" + ID
	// L'état de l'agent (au compte de l'agent, root si on le lui demande,
	// 0700). Hors de StateDir : sur la machine openCloud, StateDir appartient
	// au compte du serveur, qui pourrait renommer un dossier dessous et en
	// poser un autre.
	AgentStateDir = "/var/lib/" + ID + "-agent"
	// La racine des services déployés, un dossier par projet Compose ; réglable
	// par -root-dir au premier lancement de l'agent.
	WorkspaceRoot = "/srv/workspace"
	DatabaseFile  = ID + ".db"
	// La clé qui chiffre les secrets saisis dans l'interface, à côté de la
	// base et jamais dedans.
	SecretKeyFile = "secret.key" // #nosec G101 -- un nom de fichier, pas un secret.
	// La clé qui signe les ordres envoyés aux agents, à côté de secret.key :
	// sa moitié publique est épinglée par chaque agent à l'enrôlement.
	ServerKeyFile = "server.key" // #nosec G101 -- un nom de fichier, pas un secret.
	// La socket de commande, dans state_dir : serve y sert les routes du
	// fichier de l'annuaire à la commande locale, sans rien ouvrir au réseau.
	ControlSocketFile = "control.sock"
	// Le dossier des colis à destination des agents, dans state_dir : vidé au
	// démarrage, rien n'y reste plus de quelques heures.
	OutgoingDir = "outgoing"
	// Les versions importées par `release import`, dans state_dir : un
	// dossier par version, dont l'agent se met à jour sans rien télécharger
	// d'Internet.
	ReleasesDir = "releases"
	// Sur chaque machine, dans l'état de l'agent : les binaires installés
	// (AgentBinaryPrefix + version) et le lien que l'unité lance.
	AgentBinDir       = "bin"
	AgentCurrentLink  = "current"
	AgentBinaryPrefix = ID + "-"
	// Le binaire que la ligne d'installation et l'unité lancent. Ce n'est pas
	// /usr/local/bin/opencloud : ce lien, root le lancerait, et le compte de
	// l'agent peut remplacer ce qu'il vise.
	AgentCurrentPath = AgentStateDir + "/" + AgentBinDir + "/" + AgentCurrentLink
	// Le nom d'une sauvegarde est le préfixe de son plan (celui de la base est
	// ID), un instant UTC et « .tar.age » ; la clé scellée qui
	// l'accompagne porte le préfixe ci-dessous et l'empreinte.
	BackupKeyFilePrefix = ID + "-key-"

	// Le nom de la machine qui héberge le serveur, dans la base.
	LocalMachineName = ID
	// Préfixe des identifiants de transaction envoyés à Matrix.
	MatrixTransactionPrefix = ID + "-"
)

// Les fichiers d'une version publiée, et le commentaire de sa signature.
const (
	ReleaseBinaryPrefix     = ID + "-linux-"
	ReleaseSumsFile         = "SHA256SUMS"
	ReleaseSignatureFile    = ReleaseSumsFile + ".minisig"
	ReleaseSignatureComment = "signature from " + ID + " release key"
)

// SecretKeyIDDomain sépare l'identifiant d'une clé de toute autre empreinte
// de la même clé. Il entre dans le calcul de l'identifiant inscrit dans
// chaque secret chiffré : le changer rend les secrets déjà chiffrés
// illisibles.
const SecretKeyIDDomain = ID + "-key-id\x00"

// LabelPrefix est lu par l'agent sur les conteneurs. Dérivé de ID comme les
// chemins : le changer, c'est que les conteneurs déjà étiquetés ne sont plus
// lus et retombent aux niveaux Compose et nom.
const LabelPrefix = ID + "."

// AgentUnit est l'unité de l'agent : la page d'un droit qui écrit hors de
// l'état dit à l'opérateur laquelle compléter (ReadWritePaths).
const AgentUnit = ID + "-agent.service"

// AgentAccount est le compte système sous lequel l'installation recommandée
// lance l'agent : propriétaire de son état et des dossiers où ses droits
// écrivent. Son dossier personnel est AgentStateDir.
const AgentAccount = ID + "-agent"

// ServerUnit est l'unité du serveur : la restauration dit de la démarrer.
const ServerUnit = ID + ".service"

// WorkspaceMarker est le fichier témoin que l'agent pose à la création de la
// racine des services : une racine qui existe, n'est pas vide et n'en porte
// pas n'est pas à openCloud, l'agent n'y écrit rien.
const WorkspaceMarker = "." + ID

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
	// L'en-tête où le mandataire d'authentification pose le secret qu'il
	// partage avec le serveur : le mode forward ne croit l'identité que d'une
	// requête qui le porte. Le nom est dans la configuration du mandataire.
	ProxySecretHeader = "X-" + HeaderPrefix + "Proxy-Secret" // #nosec G101 -- un nom d'en-tête, pas un secret.
	// Précède le corps d'un ordre dans ce que la clé du serveur signe : une
	// signature d'ordre ne vaut pour rien d'autre.
	OrderSignatureDomain = "opencloud-order/v1\x00"
	// Précède ce que l'agent signe pour ouvrir son flux (protocole 2) : la
	// preuve ne vaut pour rien d'autre.
	AgentProofDomain = "opencloud-agent-proof/v2\x00"
)

// Les préfixes de jetons : reconnaissables dans un journal ou un
// presse-papier, jamais confondus entre eux (le jeton de ping, hb_, n'est
// pas une marque et reste dans internal/heartbeat).
const (
	TokenPrefix           = "oc"
	EnrollmentTokenScheme = TokenPrefix + "_"  // #nosec G101 -- un préfixe, pas un secret.
	APITokenScheme        = TokenPrefix + "k_" // #nosec G101 -- un préfixe, pas un secret.
)

// Début de l'empreinte de l'identifiant d'une machine, pour reconnaître
// un agent installé sur la machine du serveur.
const HostFingerprintPrefix = "opencloud-host:"
