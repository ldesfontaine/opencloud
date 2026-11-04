-- Les ancres de confiance locales d'un agent (internal/anchor).
--
-- Ce que l'agent dit de ses ancres à l'ouverture du flux, sous la forme de
-- l'en-tête (« backup.guest=101:102,… », « none » quand aucune n'est posée).
-- NULL tant qu'il n'a rien dit : un agent d'avant les ancres ne les applique
-- pas, le serveur ne juge alors aucun ordre par elles. Un reflet de son
-- fichier, pas un historique.
ALTER TABLE agents ADD COLUMN anchors TEXT;
