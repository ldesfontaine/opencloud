import { pingAgent, pinnedKeyState, useMachineActions, useServerKey, type ActionRun, type ActionState } from "../api/actions";
import type { Agent } from "../api/machines";
import { useAction } from "../hooks/useAction";
import { useNow } from "../hooks/useNow";
import { useT, type Translate } from "../i18n/context";
import { capabilityLabelKey } from "../lib/machines";
import { reenrollPath } from "../lib/rights";
import type { Tone } from "../lib/tone";
import { formatWhen } from "../lib/time";
import { AgentGate } from "./AgentGate";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "./Card";
import { Failure } from "./Failure";
import { MissingRightButton } from "./MissingRight";
import { Pill } from "./Pill";
import { Table } from "./Table";

export const stateTones: Record<ActionState, Tone> = {
  queued: "neutral",
  sent: "neutral",
  accepted: "accent",
  refused: "danger",
  succeeded: "ok",
  failed: "danger",
  expired: "warn",
};

// Les ordres d'une machine : la clé du serveur que son agent a épinglée,
// le bouton qui l'éprouve, et les derniers ordres avec leur état. Un ordre
// ne part que vers un agent actif et en ligne : hors ligne, le bouton est
// grisé avec sa raison.
export function ActionsCard({ machineID, agent }: { machineID: string; agent: Agent | null }) {
  const t = useT();
  const now = useNow();
  const runs = useMachineActions(machineID);
  const action = useAction();
  const isActive = agent !== null && agent.revoked_at === null;
  const ping = () => {
    action.run(() => pingAgent(machineID), { topic: "actions" });
  };
  const list = runs.data?.runs ?? [];
  return (
    <Card>
      <CardHeader
        title={t("action.title")}
        aside={
          isActive && (
            <AgentGate machineIDs={[machineID]}>
              {(blocked) => (
                <Button small icon="check" onClick={ping} disabled={action.busy || blocked}>
                  {t("action.ping")}
                </Button>
              )}
            </AgentGate>
          )
        }
      />
      <CardBody gap={10}>
        <p className="secondary">{t("action.subtitle")}</p>
        {agent !== null && <PinnedKey machineID={machineID} agent={agent} />}
        <Failure error={action.error} />
        {runs.error && <Failure error={runs.error} />}
      </CardBody>
      {runs.data && list.length === 0 && <Empty text={t("action.empty")} />}
      {list.length > 0 && (
        <Table>
          <thead>
            <tr>
              <th>{t("action.col_when")}</th>
              <th>{t("action.col_kind")}</th>
              <th>{t("action.col_state")}</th>
              <th className="wide-only">{t("action.col_by")}</th>
            </tr>
          </thead>
          <tbody>
            {list.map((run) => (
              <RunRow key={run.id} run={run} now={now} />
            ))}
          </tbody>
        </Table>
      )}
      <CardBody>
        <p className="secondary">{t("action.note")}</p>
      </CardBody>
    </Card>
  );
}

// La clé que l'agent dit avoir épinglée, comparée à celle de ce serveur :
// une autre clé, ou aucune, fera refuser chaque ordre. La précédente, pendant
// une rotation, est encore bonne : l'agent reçoit la neuve à sa connexion.
function PinnedKey({ machineID, agent }: { machineID: string; agent: Agent }) {
  const t = useT();
  const serverKey = useServerKey();
  if (agent.server_key_fingerprint === "") {
    return (
      <>
        <KeyValue label={t("action.server_key")} value={t("action.server_key_none")} />
        <ReenrollButton machineID={machineID} />
      </>
    );
  }
  const pinned = pinnedKeyState(serverKey.data, agent.server_key_fingerprint);
  return (
    <>
      <KeyValue label={t("action.server_key")} value={agent.server_key_fingerprint} mono />
      {pinned === "previous" && serverKey.data && <Note tone="warn">{t("action.server_key_previous", serverKey.data.fingerprint)}</Note>}
      {pinned === "differs" && serverKey.data && (
        <Note tone="danger">
          <p>{t("action.server_key_differs", serverKey.data.fingerprint)}</p>
          <ReenrollButton machineID={machineID} />
        </Note>
      )}
    </>
  );
}

// Ce qui se corrige par un ré-enrôlement sans droit à cocher (la clé du serveur).
function ReenrollButton({ machineID }: { machineID: string }) {
  const t = useT();
  return (
    <Button small icon="refresh" to={reenrollPath(machineID)}>
      {t("machine.reenroll")}
    </Button>
  );
}

function RunRow({ run, now }: { run: ActionRun; now: number }) {
  const t = useT();
  const output = run.output.map((line) => line.line).join("\n");
  const missing = missingRightOf(run.reason);
  return (
    <tr>
      <td>{formatWhen(run.requested_at, now)}</td>
      <td>{t(`action.kind_${run.kind.split(".").join("_")}`)}</td>
      <td>
        <Pill tone={stateTones[run.state]}>{t(`action.state_${run.state}`)}</Pill>
        {run.reason !== "" && <div className="secondary">{reasonText(t, run.reason)}</div>}
        {missing !== null && run.machine_id !== null && <MissingRightButton machineID={run.machine_id} right={missing} />}
        {run.reason === "server_key_missing" && run.machine_id !== null && <ReenrollButton machineID={run.machine_id} />}
        {output !== "" && <pre className="secondary">{output}</pre>}
      </td>
      <td className="wide-only">{run.requested_by}</td>
    </tr>
  );
}

// Le droit qui manque quand l'agent a refusé l'ordre pour cela (right_missing:<droit>).
function missingRightOf(reason: string): string | null {
  const [code, right] = reason.split(":");
  return code === "right_missing" && right !== undefined ? right : null;
}

// La raison d'un refus ; right_missing porte après « : » le droit qui manque.
export function reasonText(t: Translate, reason: string): string {
  const [code, right] = reason.split(":");
  if (right !== undefined) {
    return t(`action.reason_${code}`, t(capabilityLabelKey(right)));
  }
  return t(`action.reason_${code}`);
}
