-- Le retour arrière automatique d'une mise à jour d'agent.
--
-- La version du retour arrière que l'agent sait faire, redite à chaque
-- ouverture du flux : 0 tant qu'il ne le dit pas (un agent d'avant, ou qui ne
-- tourne pas sous le lanceur stable). Le serveur refuse agent.update pour un
-- agent à 0 : jamais de mise à jour sans retour arrière.
ALTER TABLE agents ADD COLUMN rollback_version INTEGER NOT NULL DEFAULT 0 CHECK (rollback_version >= 0);

-- Une machine à mettre à jour en dernier : elle porte le chemin des ordres
-- (proxy, VPN…), le couper avant les autres couperait l'ordre suivant. Réglé
-- par l'opérateur seul.
ALTER TABLE machines ADD COLUMN update_last INTEGER NOT NULL DEFAULT 0 CHECK (update_last IN (0, 1));
