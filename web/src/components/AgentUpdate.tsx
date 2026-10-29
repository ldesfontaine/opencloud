import { rollbackAgent, setUpdateLast, updateAgent, useAgentMigration, useAgentUpdateOffer, useMachineActions } from "../api/actions";
import type { Agent } from "../api/machines";
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { agentUpdateState, type AgentUpdateState } from "../lib/agentUpdate";
import { Button } from "./Button";
import { KeyValue, Note } from "./Card";
import { ConfirmInline } from "./ConfirmInline";
import { Failure } from "./Failure";

// La mise à jour de l'agent : où en est la dernière (en attente de
// confirmation, confirmée, revenue en arrière toute seule), le bouton quand la
// version du serveur est importée et que l'agent peut la recevoir, sinon la
// raison en une ligne, le retour arrière manuel et le drapeau « en dernier ».
// Une machine à jour n'a rien à dire de plus.
export function AgentUpdate({ machineID, agent, updateLast, online }: { machineID: string; agent: Agent; updateLast: boolean; online: boolean }) {
  const t = useT();
  const offer = useAgentUpdateOffer(machineID);
  const actions = useMachineActions(machineID);
  const action = useAction();
  const current = offer.data;
  const state = agentUpdateState(actions.data?.runs ?? []);
  if (current === null) {
    return offer.error ? <Failure error={offer.error} /> : null;
  }
  const update = () => {
    action.run(() => updateAgent(machineID), {
      confirm: t("machine.agent_update_confirm", current.current, current.target),
      topic: "actions",
    });
  };
  const rollback = () => {
    action.run(() => rollbackAgent(machineID), { topic: "actions" });
  };
  const toggleUpdateLast = () => {
    action.run(() => setUpdateLast(machineID, !updateLast), { topic: "machines" });
  };
  return (
    <>
      <Failure error={action.error} />
      <AgentUpdateState state={state} />
      <KeyValue
        label={t("machine.agent_rollback_capable")}
        value={agent.rollback_capable ? t("machine.agent_rollback_capable_yes") : t("machine.agent_rollback_capable_no")}
      />
      {current.available && (
        <>
          <div>
            <Button small icon="refresh" onClick={update} disabled={action.busy}>
              {t("machine.agent_update", current.target)}
            </Button>
          </div>
          {current.reason === "right_unavailable" && <p className="secondary">{t("machine.agent_update_reason_right_unavailable")}</p>}
        </>
      )}
      {!current.available && current.reason === "rollback_unavailable" && <AgentMigration />}
      {!current.available && current.reason !== "" && current.reason !== "same_version" && current.reason !== "rollback_unavailable" && (
        <p className="secondary">{t(`machine.agent_update_reason_${current.reason}`, current.target, current.arch)}</p>
      )}
      {agent.rollback_capable && online && (
        <div>
          <ConfirmInline small icon="refresh" question={t("machine.agent_rollback_confirm")} onConfirm={rollback} disabled={action.busy}>
            {t("machine.agent_rollback")}
          </ConfirmInline>
        </div>
      )}
      <div>
        <Button small variant={updateLast ? "primary" : "secondary"} onClick={toggleUpdateLast} disabled={action.busy} aria-pressed={updateLast}>
          {updateLast ? t("machine.agent_update_last") : t("machine.agent_update_last_set")}
        </Button>
      </div>
      <p className="secondary">{t("machine.agent_update_last_text")}</p>
    </>
  );
}

// La dernière mise à jour, en une ligne : seul un retour arrière, automatique
// ou raté, se dit en alerte ; le reste est une information.
function AgentUpdateState({ state }: { state: AgentUpdateState | null }) {
  const t = useT();
  if (state === null) {
    return null;
  }
  switch (state.kind) {
    case "pending":
      return <Note tone="warn">{t("machine.agent_update_state_pending")}</Note>;
    case "confirmed":
      return <p className="secondary">{t("machine.agent_update_state_confirmed")}</p>;
    case "rolled_back":
      return <Note tone="warn">{t("machine.agent_update_state_rolled_back", state.target, t(`machine.agent_update_cause_${state.cause}`))}</Note>;
    case "rollback_failed":
      return <Note tone="danger">{t("machine.agent_update_state_rollback_failed")}</Note>;
  }
}

// Les lignes à coller sur une machine dont l'agent n'a pas le retour arrière
// automatique : l'interface les produit, avec la version de ce serveur.
function AgentMigration() {
  const t = useT();
  const migration = useAgentMigration();
  return (
    <Note tone="warn">
      <p>{t("machine.agent_update_reason_rollback_unavailable")}</p>
      <strong>{t("machine.agent_migration_title")}</strong>
      {migration.data !== null && (
        <>
          <p className="secondary">{t("machine.agent_migration_text", migration.data.version)}</p>
          <pre className="mono scroll-x">{migration.data.commands}</pre>
        </>
      )}
    </Note>
  );
}
