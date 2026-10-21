-- Un docker kill sort en 137, comme un docker stop dont le délai expire :
-- seul l'agent, qui voit si un arrêt a été demandé juste avant, les
-- distingue. Gardé sur la fiche et sur la transition d'arrêt.

ALTER TABLE services ADD COLUMN killed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE service_transitions ADD COLUMN killed INTEGER NOT NULL DEFAULT 0;
