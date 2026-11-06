-- Les familles d'alertes qu'un canal reçoit, séparées par des virgules, dans
-- l'ordre du catalogue (internal/alert/category.go). Vide : toutes, ce que
-- reçoivent déjà les canaux existants. Pas de CHECK : le catalogue grandit
-- avec le code, et la validation est celle du moteur.
ALTER TABLE alert_channels ADD COLUMN categories TEXT NOT NULL DEFAULT '';
