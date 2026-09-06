-- Ce que la sonde « Tester l'accès » a constaté sur une machine
-- (02-roles.md, le statut à quatre états). Elle tourne périodiquement, en
-- silence : ces trois colonnes sont tout ce qu'elle écrit.
--
-- probed_at vide : la machine n'a jamais été sondée. probe_state vide : rien
-- n'est encore connu de son accès, ce qui n'est pas « injoignable ».
ALTER TABLE machines ADD COLUMN probed_at   TEXT NOT NULL DEFAULT '';
ALTER TABLE machines ADD COLUMN probe_state TEXT NOT NULL DEFAULT ''
                    CHECK (probe_state IN ('', 'reachable', 'ssh-failed', 'launcher-failed'));
-- Ce qui a échoué, borné, tel qu'on l'affiche à l'opérateur.
ALTER TABLE machines ADD COLUMN probe_note  TEXT NOT NULL DEFAULT '';
