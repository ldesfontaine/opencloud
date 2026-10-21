-- La portée d'un jeton MCP : read, les lectures seules ; write, les
-- lectures et les écritures. Un jeton d'API se crée désormais en read par
-- défaut ; ceux qui existent déjà passent en write, pour ne rien casser
-- chez qui s'en sert. Un jeton OAuth est toujours en write : l'opérateur
-- l'a obtenu derrière /oauth/authorize.
ALTER TABLE mcp_tokens ADD COLUMN scope TEXT NOT NULL DEFAULT 'write' CHECK (scope IN ('read', 'write'));
