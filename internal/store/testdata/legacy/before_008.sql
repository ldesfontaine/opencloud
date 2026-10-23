-- Les lignes d'un schéma d'avant la 008 : un client OAuth, un code, des jetons
-- d'API (en écriture, en lecture, révoqué), un jeton d'accès et un de
-- rafraîchissement. Lues par TestMigration008_...
INSERT INTO mcp_clients (id, secret_hash, secret_prefix, redirect_uris, created_at) VALUES ('client', 'h', 'p', 'https://claude.ai/cb', 1);
INSERT INTO mcp_codes (code_hash, client_id, redirect_uri, challenge, scope, expires_at, used, created_at) VALUES ('c1', 'client', 'https://claude.ai/cb', 'x', 'write', 100, 0, 1);
INSERT INTO mcp_tokens (id, token_hash, token_prefix, kind, scope, name, family_id, created_at, expires_at, last_used_at, revoked_at) VALUES ('tok_write', 'hash_write', 'ock_aaaa', 'api', 'write', 'ancien', '', 10, NULL, NULL, NULL);
INSERT INTO mcp_tokens (id, token_hash, token_prefix, kind, scope, name, family_id, created_at, expires_at, last_used_at, revoked_at) VALUES ('tok_read', 'hash_read', 'ock_bbbb', 'api', 'read', 'mcp-maison', '', 20, 5000, 4000, NULL);
INSERT INTO mcp_tokens (id, token_hash, token_prefix, kind, scope, name, family_id, created_at, expires_at, last_used_at, revoked_at) VALUES ('tok_revoked', 'hash_revoked', 'ock_cccc', 'api', 'write', 'révoqué', '', 30, NULL, NULL, 99);
INSERT INTO mcp_tokens (id, token_hash, token_prefix, kind, scope, name, family_id, created_at, expires_at, last_used_at, revoked_at) VALUES ('tok_access', 'hash_access', 'oc_dddd', 'access', 'write', '', 'fam', 40, 3600, NULL, NULL);
INSERT INTO mcp_tokens (id, token_hash, token_prefix, kind, scope, name, family_id, created_at, expires_at, last_used_at, revoked_at) VALUES ('tok_refresh', 'hash_refresh', 'oc_eeee', 'refresh', 'write', '', 'fam', 50, 86400, NULL, NULL);
