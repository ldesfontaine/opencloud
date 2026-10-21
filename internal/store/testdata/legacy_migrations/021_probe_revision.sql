-- L'édition d'une sonde sur place : l'historique reste, mais un réglage qui
-- change ce que la sonde mesure remet son état à « Nouveau ». Un essai
-- parti avec l'ancien réglage peut encore arriver après l'édition ; sans
-- trace de l'instant de la révision, il écraserait l'état tout juste remis à
-- zéro. En millisecondes, comme l'horodatage des essais : un essai de
-- l'ancien réglage pris dans la seconde de l'édition ne doit pas passer pour
-- un essai du nouveau. NULL : la sonde n'a jamais été révisée.
ALTER TABLE probes ADD COLUMN revised_at INTEGER;
