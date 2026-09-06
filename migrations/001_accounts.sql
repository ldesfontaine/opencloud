-- Les comptes de l'interface. Un seul opérateur au départ ; le modèle
-- n'interdit pas d'en ajouter. Les colonnes totp_* préparent la 2FA
-- (08-securite-et-secrets.md), le code ne l'implémente pas encore.
CREATE TABLE accounts (
  id                   INTEGER PRIMARY KEY,
  username             TEXT    NOT NULL UNIQUE,
  password_hash        TEXT    NOT NULL,
  must_change_password INTEGER NOT NULL DEFAULT 0,
  totp_secret          TEXT,
  totp_enabled_at      TEXT,
  created_at           TEXT    NOT NULL,
  updated_at           TEXT    NOT NULL
);
