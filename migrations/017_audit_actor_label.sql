-- Le nom que portait l'acteur à l'instant du geste : pour un jeton d'API, le
-- nom du jeton, recopié (le jeton peut être révoqué ensuite, ou un autre
-- porter son nom). NULL pour les lignes d'avant et pour les acteurs qui n'en
-- ont pas : elles se lisent comme avant, sans jointure.
ALTER TABLE audit_events ADD COLUMN actor_label TEXT;

-- Le déclencheur d'ajout seul garde aussi la colonne neuve.
DROP TRIGGER audit_events_append_only;

CREATE TRIGGER audit_events_append_only
BEFORE UPDATE ON audit_events
WHEN NEW.apply_id IS NOT NULL
    OR NEW.id IS NOT OLD.id
    OR NEW.at IS NOT OLD.at
    OR NEW.actor_kind IS NOT OLD.actor_kind
    OR NEW.actor_name IS NOT OLD.actor_name
    OR NEW.actor_label IS NOT OLD.actor_label
    OR NEW.action IS NOT OLD.action
    OR NEW.object_kind IS NOT OLD.object_kind
    OR NEW.object_id IS NOT OLD.object_id
    OR NEW.object_name IS NOT OLD.object_name
    OR NEW.details IS NOT OLD.details
BEGIN
    SELECT RAISE(ABORT, 'audit_events is append-only');
END;
