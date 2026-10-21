-- L'accès d'un agent IA par MCP : un seul client OAuth, ses codes
-- d'autorisation, ses jetons d'accès et de rafraîchissement, et les
-- jetons d'API que l'opérateur crée pour un script. Codes, jetons et
-- secret ne sont stockés que hachés : une copie de la base n'ouvre rien.
-- Horodatages en secondes depuis l'époque Unix.

-- Une seule ligne : le client existe quand MCP est activé, et disparaît
-- quand il est désactivé. Les redirect_uri déclarés s'ajoutent à la
-- boucle locale, toujours acceptée ; un par ligne.
CREATE TABLE mcp_clients (
    id            TEXT PRIMARY KEY,
    secret_hash   TEXT NOT NULL,
    secret_prefix TEXT NOT NULL,
    redirect_uris TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);

-- Un code sert une fois, dix minutes au plus : la consommation est un
-- UPDATE conditionnel, un seul échange concurrent gagne.
CREATE TABLE mcp_codes (
    code_hash    TEXT PRIMARY KEY,
    client_id    TEXT NOT NULL,
    redirect_uri TEXT NOT NULL,
    challenge    TEXT NOT NULL,
    expires_at   INTEGER NOT NULL,
    used         INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL
);

-- Trois sortes : access (1 h), refresh (30 j), api (sans échéance). Un
-- jeton de rafraîchissement consommé est révoqué et remplacé ; toute la
-- famille d'une même autorisation est révoquée si l'un d'eux est rejoué.
-- Un jeton révoqué reste jusqu'à son échéance, c'est ce qui permet de
-- voir le rejeu ; la purge l'efface ensuite.
CREATE TABLE mcp_tokens (
    id           TEXT PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('access', 'refresh', 'api')),
    name         TEXT NOT NULL DEFAULT '',
    family_id    TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER,
    last_used_at INTEGER,
    revoked_at   INTEGER
);

CREATE INDEX mcp_tokens_family ON mcp_tokens (family_id) WHERE family_id != '';
CREATE INDEX mcp_tokens_expires ON mcp_tokens (expires_at) WHERE expires_at IS NOT NULL;
