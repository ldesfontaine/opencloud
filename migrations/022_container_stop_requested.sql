-- Un arrêt demandé (docker stop, restart, compose) qui sort en code non nul :
-- le conteneur a reçu le signal d'arrêt puis a quitté de lui-même ou après le
-- délai. Ce n'est pas un plantage ; seul ce fait le distingue d'un plantage.
ALTER TABLE containers ADD COLUMN stop_requested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE container_events ADD COLUMN stop_requested INTEGER NOT NULL DEFAULT 0;
