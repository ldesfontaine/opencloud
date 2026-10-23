-- Un conteneur tué par le noyau faute de mémoire sort en 137, comme un
-- docker stop : seul OOMKilled les distingue. Gardé sur la fiche et sur la
-- transition d'arrêt. Numéro 013 : 012 est pris par l'authentification.

ALTER TABLE services ADD COLUMN oom_killed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE service_transitions ADD COLUMN oom_killed INTEGER NOT NULL DEFAULT 0;
