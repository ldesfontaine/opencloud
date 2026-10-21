-- Les labels <préfixe>.service et <préfixe>.unit du conteneur, tels que
-- l'agent les a lus (préfixe retiré) : le niveau 1 du rattachement. Écrits
-- par qui lance le conteneur, ou hérités de son image, jamais par
-- l'opérateur dans openCloud : ils désignent une unité conteneur déjà
-- déclarée sur la même machine et ne créent ni ne modifient rien dans
-- l'annuaire. Vides : pas de label, ou un agent qui ne les lit pas encore.
-- label_invalid : le conteneur porte au moins un label de la marque que
-- l'agent a dû écarter (clé en majuscules, valeur trop longue…) et aucun de
-- ceux qu'openCloud lit. Il se compte comme un conteneur étiqueté, donc il ne
-- se replie ni sur Compose ni sur le nom : il est « label orphelin ».
-- match_level gagne la valeur 'label' (pas de CHECK, voir 023). Ajout pur :
-- un binaire précédent écrit ses fiches sans ces colonnes et les ignore.
ALTER TABLE containers ADD COLUMN label_service TEXT NOT NULL DEFAULT '';
ALTER TABLE containers ADD COLUMN label_unit    TEXT NOT NULL DEFAULT '';
ALTER TABLE containers ADD COLUMN label_invalid INTEGER NOT NULL DEFAULT 0;
