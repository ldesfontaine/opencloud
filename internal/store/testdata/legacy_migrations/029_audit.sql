-- Le journal d'audit : qui a fait quoi, quand, sur quel objet déclaré ou
-- quelle autorité. Une ligne par geste réussi, écrite par internal/audit ;
-- les écritures d'observation (signaux, mesures, résultats de sondes) n'y
-- vont pas. Sans clé étrangère vers l'objet : son nom est recopié, la ligne
-- survit à sa suppression. Le passage d'apply qui a écrit l'objet reste lié,
-- en set null. Ajout seul : aucune ligne ne se modifie (le déclencheur
-- refuse, hors la mise à nul du lien par la clé étrangère) ; une ligne ne
-- part que par la purge à l'âge (audit.Retention, un an). Pas de CHECK sur
-- les listes fermées : le code les tient, comme pour applies. details est un
-- objet JSON plat de textes, jamais un secret. Secondes Unix.
CREATE TABLE audit_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT, -- jamais réutilisé : sert de curseur
    at          INTEGER NOT NULL,
    actor_kind  TEXT NOT NULL,                     -- operator | mcp | cli | rule | agent | unknown
    actor_name  TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,                     -- <object_kind>.<verbe>
    object_kind TEXT NOT NULL,
    object_id   TEXT NOT NULL DEFAULT '',
    object_name TEXT NOT NULL DEFAULT '',          -- recopié au moment du geste
    details     TEXT NOT NULL DEFAULT '{}',
    apply_id    TEXT REFERENCES applies (id) ON DELETE SET NULL
);

CREATE INDEX audit_events_at ON audit_events (at);
CREATE INDEX audit_events_object ON audit_events (object_kind, object_id, id);
CREATE INDEX audit_events_apply ON audit_events (apply_id) WHERE apply_id IS NOT NULL;

CREATE TRIGGER audit_events_append_only
BEFORE UPDATE ON audit_events
WHEN NEW.apply_id IS NOT NULL
    OR NEW.id IS NOT OLD.id
    OR NEW.at IS NOT OLD.at
    OR NEW.actor_kind IS NOT OLD.actor_kind
    OR NEW.actor_name IS NOT OLD.actor_name
    OR NEW.action IS NOT OLD.action
    OR NEW.object_kind IS NOT OLD.object_kind
    OR NEW.object_id IS NOT OLD.object_id
    OR NEW.object_name IS NOT OLD.object_name
    OR NEW.details IS NOT OLD.details
BEGIN
    SELECT RAISE(ABORT, 'audit_events is append-only');
END;
