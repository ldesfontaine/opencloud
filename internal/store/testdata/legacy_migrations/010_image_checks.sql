-- Les mises à jour des images. La fiche gagne ce que Compose dit de son
-- service (nom, dossier, fichier), de quoi fabriquer la commande à
-- copier ; et la politique de l'opérateur : suivre ('' par défaut),
-- épingler (vérifiée, jamais montrée), exclure (jamais interrogée).

ALTER TABLE services ADD COLUMN compose_service TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN compose_dir     TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN compose_file    TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN update_policy   TEXT NOT NULL DEFAULT '' CHECK (update_policy IN ('', 'pinned', 'excluded'));

-- Le dernier constat de l'agent par image d'une machine, écrasé à chaque
-- vérification : l'empreinte tirée, celle que le tag pointe sur le
-- registre, le tag plus récent de même forme. Le type est déduit par le
-- serveur à l'écriture. Une image plus vérifiée depuis 30 jours est
-- purgée ; la machine retirée emporte tout. Horodatages en secondes.
CREATE TABLE image_checks (
    machine_id    TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    image         TEXT NOT NULL,
    checked_at    INTEGER NOT NULL,
    outcome       TEXT NOT NULL CHECK (outcome IN ('ok', 'local', 'unreachable', 'unauthorized', 'not_found', 'unsupported')),
    local_digest  TEXT NOT NULL DEFAULT '',
    remote_digest TEXT NOT NULL DEFAULT '',
    newer_tag     TEXT NOT NULL DEFAULT '',
    newer_digest  TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'major', 'minor', 'patch', 'digest')),
    PRIMARY KEY (machine_id, image)
) WITHOUT ROWID;

CREATE INDEX image_checks_checked ON image_checks (checked_at);
