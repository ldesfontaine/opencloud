-- L'accès de l'opérateur à l'interface : une seule ligne, le mode et,
-- en mode basic, l'identifiant et l'empreinte bcrypt du mot de passe.
-- Le mot de passe en clair n'est jamais stocké. « forward » est réservé
-- au mode futur derrière un mandataire d'authentification (Authelia) ;
-- rien ne l'écrit encore. Horodatages en secondes depuis l'époque Unix.
CREATE TABLE auth_settings (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    mode          TEXT NOT NULL CHECK (mode IN ('none', 'basic', 'forward')),
    username      TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    enabled_at    INTEGER,
    updated_at    INTEGER NOT NULL
);
