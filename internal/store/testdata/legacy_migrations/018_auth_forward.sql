-- Le mode forward : openCloud derrière un mandataire qui authentifie
-- (Authelia) et transmet l'identité dans un en-tête. On garde le nom de cet
-- en-tête, vide hors de ce mode ; l'identité elle-même n'est jamais
-- stockée, elle arrive avec chaque requête.
ALTER TABLE auth_settings ADD COLUMN forward_header TEXT NOT NULL DEFAULT '';
