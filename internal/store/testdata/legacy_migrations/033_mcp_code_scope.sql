-- La portée qu'un code d'autorisation porte jusqu'à l'échange : celle que
-- l'opérateur a consentie sur la page de consentement, read par défaut.
-- Les codes d'avant vivent dix minutes au plus et passent en read.
ALTER TABLE mcp_codes ADD COLUMN scope TEXT NOT NULL DEFAULT 'read' CHECK (scope IN ('read', 'write'));
