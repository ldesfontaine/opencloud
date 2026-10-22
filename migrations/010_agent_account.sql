-- Le compte sous lequel l'agent tourne, redit à chaque ouverture du flux :
-- nul tant qu'un agent ne l'a pas dit (un agent d'avant). Le nom peut
-- manquer (compte sans nom lisible) ; l'uid suffit à dire « root ».
ALTER TABLE agents ADD COLUMN run_as_uid  INTEGER CHECK (run_as_uid IS NULL OR run_as_uid BETWEEN 0 AND 4294967295);
ALTER TABLE agents ADD COLUMN run_as_name TEXT NOT NULL DEFAULT '';

-- Pourquoi un droit coché ne peut pas s'exercer sur la machine, vide s'il
-- le peut : rapporté avec les droits, remplacé avec eux.
ALTER TABLE agent_capabilities ADD COLUMN unavailable TEXT NOT NULL DEFAULT '';
