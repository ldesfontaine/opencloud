-- Les demandes de verbe à confirmation : un jeton d'API qui appelle un geste
-- qui ne se défait pas (supprimer une machine) n'agit pas, il dépose une
-- demande. L'opérateur la confirme ou la refuse dans l'interface ; confirmée,
-- elle rejoue la route d'opérateur avec les paramètres et le corps gardés ici.
--
-- Une demande vit quinze minutes (expires_at). Révoquer le jeton emporte ses
-- demandes : un jeton volé puis révoqué ne laisse rien à confirmer. Le journal
-- d'audit, lui, garde la trace. token_name est le nom du jeton à l'instant de
-- la demande, pour la montrer sans relire le jeton.
CREATE TABLE verb_requests (
    id           TEXT PRIMARY KEY,
    verb         TEXT NOT NULL,
    object_name  TEXT NOT NULL,
    params       TEXT NOT NULL,
    body         TEXT NOT NULL,
    token_id     TEXT NOT NULL REFERENCES api_tokens (id) ON DELETE CASCADE,
    token_name   TEXT NOT NULL,
    on_behalf_of TEXT NOT NULL,
    state        TEXT NOT NULL CHECK (state IN ('awaiting_confirmation', 'confirmed', 'rejected', 'expired')),
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
);

CREATE INDEX verb_requests_state ON verb_requests (state, expires_at);
CREATE INDEX verb_requests_token ON verb_requests (token_id);
