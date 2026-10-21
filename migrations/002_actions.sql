-- Les ordres envoyés aux agents (#146) : chaque ordre est écrit ici avant de
-- partir, signé par la clé du serveur, et suivi jusqu'à sa réponse.

-- Ce que l'agent dit avoir épinglé de la clé du serveur (son empreinte,
-- redite à chaque ouverture du flux), et le dernier rang d'ordre donné à cet
-- agent : le rang croît par agent, l'agent refuse un rang déjà vu. Gardé sur
-- l'agent et non tiré des runs : la purge des runs ne doit pas le faire
-- reculer.
ALTER TABLE agents ADD COLUMN server_key_fingerprint TEXT    NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN last_order_seq         INTEGER NOT NULL DEFAULT 0;

-- Un ordre et son exécution. Une trace : il survit à sa machine et à son
-- agent (SET NULL, nom recopié). action vient de la liste fermée du code
-- (agentwire.Kind) ; params ne porte jamais de secret. state : queued, sent,
-- accepted, refused, succeeded, failed, expired. refusal_reason dit pourquoi
-- l'ordre n'a pas abouti : la raison de l'agent, ou « undelivered ».
-- requested_by recopie l'acteur du journal d'audit (« operator:lucas »).
CREATE TABLE action_runs (
    id             TEXT    PRIMARY KEY,
    action         TEXT    NOT NULL,
    params         TEXT    NOT NULL DEFAULT '{}',
    machine_id     TEXT    REFERENCES machines (id) ON DELETE SET NULL,
    machine_name   TEXT    NOT NULL,
    agent_id       TEXT    REFERENCES agents (id) ON DELETE SET NULL,
    state          TEXT    NOT NULL,
    refusal_reason TEXT    NOT NULL DEFAULT '',
    not_after      INTEGER NOT NULL,
    seq            INTEGER NOT NULL,
    requested_by   TEXT    NOT NULL,
    requested_at   INTEGER NOT NULL,
    finished_at    INTEGER
);

CREATE INDEX action_runs_machine ON action_runs (machine_id, requested_at);
CREATE INDEX action_runs_requested ON action_runs (requested_at);
-- Un rang ne sert qu'une fois par agent.
CREATE UNIQUE INDEX action_runs_agent_seq ON action_runs (agent_id, seq);
-- Ce que la boucle de fond fait expirer.
CREATE INDEX action_runs_pending ON action_runs (not_after) WHERE state IN ('queued', 'sent');

-- La sortie d'un run, bornée par l'agent et par le serveur ; elle part avec
-- son run.
CREATE TABLE run_logs (
    run_id TEXT    NOT NULL REFERENCES action_runs (id) ON DELETE CASCADE,
    seq    INTEGER NOT NULL,
    at     INTEGER NOT NULL,
    stream TEXT    NOT NULL,
    line   TEXT    NOT NULL,
    PRIMARY KEY (run_id, seq)
);
