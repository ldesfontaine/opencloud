-- Les réglages que l'opérateur change dans l'interface : une seule ligne.
-- Ils vivaient dans settings.toml, sous le répertoire d'état ; serve reprend
-- ce fichier au démarrage puis l'efface (internal/settings). config.toml,
-- lui, reste à l'administrateur, en lecture seule pour le serveur.
-- Vide ou zéro : rien n'est réglé, chaque composant prend sa valeur par
-- défaut. Le mot de passe du relais est en clair, comme il l'était dans le
-- fichier en 0600 ; il sera chiffré avec les autres secrets saisis dans
-- l'interface. Horodatage en secondes depuis l'époque Unix.
CREATE TABLE settings (
    id                     INTEGER PRIMARY KEY CHECK (id = 1),
    language               TEXT    NOT NULL DEFAULT '',
    status_title           TEXT    NOT NULL DEFAULT '',
    status_announcement    TEXT    NOT NULL DEFAULT '',
    status_language        TEXT    NOT NULL DEFAULT '',
    disk_attention_percent INTEGER NOT NULL DEFAULT 0,
    disk_danger_percent    INTEGER NOT NULL DEFAULT 0,
    mail_host              TEXT    NOT NULL DEFAULT '',
    mail_port              INTEGER NOT NULL DEFAULT 0,
    mail_tls               TEXT    NOT NULL DEFAULT '',
    mail_username          TEXT    NOT NULL DEFAULT '',
    mail_password          TEXT    NOT NULL DEFAULT '',
    mail_from              TEXT    NOT NULL DEFAULT '',
    lifecycle_disabled     INTEGER NOT NULL DEFAULT 0,
    updated_at             INTEGER NOT NULL
);
