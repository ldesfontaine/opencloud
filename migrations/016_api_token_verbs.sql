-- Un jeton d'API en écriture ne déclenche que les verbes que l'opérateur a
-- cochés à sa création (internal/verb). Une ligne par verbe porté ; aucune
-- ligne, aucune action. Le verbe est un identifiant du registre, vérifié à
-- la création : la base ne le juge pas, elle n'a pas la liste. Révoquer le
-- jeton emporte ses verbes.
CREATE TABLE api_token_verbs (
    token_id TEXT NOT NULL REFERENCES api_tokens (id) ON DELETE CASCADE,
    verb     TEXT NOT NULL,
    PRIMARY KEY (token_id, verb)
) WITHOUT ROWID;
