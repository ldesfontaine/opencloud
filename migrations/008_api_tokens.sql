-- Le MCP a quitté openCloud (#224) : un serveur MCP à part, mcp-maison,
-- lit l'API avec un jeton d'API. Restent les jetons d'API ; le client OAuth,
-- ses codes, ses jetons d'accès et de rafraîchissement partent. Un jeton
-- vivant garde son identifiant, son empreinte et son échéance : celui que
-- mcp-maison porte déjà continue de servir, en lecture.

-- Un jeton présenté en Authorization: Bearer sur l'API. Révoquer l'efface :
-- présenté ensuite, il est inconnu, comme un faux. expires_at NULL : sans
-- échéance, choisi à la création. scope : read seul pour l'instant ; write
-- attend les actions de l'API (#153, étape 5), sans reconstruire la table.
CREATE TABLE api_tokens (
    id           TEXT    PRIMARY KEY,
    token_hash   TEXT    NOT NULL UNIQUE,
    token_prefix TEXT    NOT NULL,
    scope        TEXT    NOT NULL DEFAULT 'read' CHECK (scope IN ('read', 'write')),
    name         TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER,
    last_used_at INTEGER
);

CREATE INDEX api_tokens_expires ON api_tokens (expires_at) WHERE expires_at IS NOT NULL;

-- Les jetons d'API vivants, en lecture : le MCP HTTP ne servait aucune
-- écriture (lecture seule pendant la refonte), un jeton write n'écrivait rien.
INSERT INTO api_tokens (id, token_hash, token_prefix, scope, name, created_at, expires_at, last_used_at)
SELECT id, token_hash, token_prefix, 'read', name, created_at, expires_at, last_used_at
FROM mcp_tokens
WHERE kind = 'api' AND revoked_at IS NULL;

DROP TABLE mcp_tokens;
DROP TABLE mcp_codes;
DROP TABLE mcp_clients;
