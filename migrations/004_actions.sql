-- Le journal de transaction des actions (05-execution.md). Une ligne est
-- écrite « prepared » avant tout dépôt ; toute ligne « prepared » ou
-- « running » retrouvée au démarrage déclenche une reprise.
CREATE TABLE actions (
  id              TEXT    PRIMARY KEY,
  machine_id      TEXT    NOT NULL REFERENCES machines(id),
  kind            TEXT    NOT NULL,
  params          TEXT    NOT NULL,
  state           TEXT    NOT NULL
                          CHECK (state IN ('prepared', 'running', 'applied', 'failed', 'refused')),
  script_digest   TEXT    NOT NULL,
  unit_name       TEXT    NOT NULL,
  timeout_seconds INTEGER NOT NULL,
  created_at      TEXT    NOT NULL,
  launched_at     TEXT,
  finished_at     TEXT,
  exit_code       INTEGER,
  -- La dernière ligne « résultat: » vue, le constat final du script.
  result          TEXT    NOT NULL DEFAULT '',
  -- Ce que le runner veut dire à l'opérateur quand la sortie ne suffit pas.
  note            TEXT    NOT NULL DEFAULT '',
  -- Le curseur journald de la dernière ligne stockée : la reprise repart de là.
  last_cursor     TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX actions_machine_created ON actions(machine_id, created_at);
CREATE INDEX actions_state ON actions(state);

-- La sortie du script, une ligne par ligne. seq est posé par openCloud, il
-- ordonne l'affichage et permet au direct de reprendre sans doublon ni trou.
CREATE TABLE action_lines (
  action_id TEXT    NOT NULL REFERENCES actions(id) ON DELETE CASCADE,
  seq       INTEGER NOT NULL,
  at        TEXT    NOT NULL,
  text      TEXT    NOT NULL,
  PRIMARY KEY (action_id, seq)
);
