-- « À vérifier » et la provenance « migration » venaient de la reprise de
-- l'existant (l'ancienne 024), retirée par le schéma unique : plus rien ne
-- crée un service sans l'opérateur. declared_by reste ; la découverte
-- automatique, le jour où elle viendra, y ajoutera sa valeur.
UPDATE declared_services SET declared_by = 'operator' WHERE declared_by = 'migration';
ALTER TABLE declared_services DROP COLUMN needs_review;
