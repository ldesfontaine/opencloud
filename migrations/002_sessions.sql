-- Les sessions ouvertes. On ne garde que l'empreinte du jeton : une lecture
-- de la base ne permet pas de se faire passer pour l'opérateur.
CREATE TABLE sessions (
  token_hash TEXT    PRIMARY KEY,
  account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  created_at TEXT    NOT NULL,
  expires_at TEXT    NOT NULL
);

CREATE INDEX sessions_account_id ON sessions(account_id);
