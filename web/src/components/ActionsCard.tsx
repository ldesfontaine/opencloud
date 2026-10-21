import { pingAgent, useMachineActions, useServerKey, type ActionRun, type ActionState } from "../api/actions";
import type { Agent } from "../api/machines";
import { useAction } from "../hooks/useAction";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import type { Tone } from "../lib/tone";
import { formatWhen } from "../lib/time";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "./Card";
import { Failure } from "./Failure";
import { Pill } from "./Pill";
import { Table } from "./Table";

const stateTones: Record<ActionState, Tone> = {
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
// ne part que vers un agent actif et en ligne : le bouton n'est là que pour
// lui.
export function ActionsCard({ machineID, agent, online }: { machineID: string; agent: Agent | null; online: boolean }) {
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
          isActive &&
          online && (
            <Button small icon="check" onClick={ping} disabled={action.busy}>
              {t("action.ping")}
            </Button>
          )
        }
      />
      <CardBody gap={10}>
        <p className="secondary">{t("action.subtitle")}</p>
        {agent !== null && <PinnedKey agent={agent} />}
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
// une autre clé, ou aucune, fera refuser chaque ordre.
function PinnedKey({ agent }: { agent: Agent }) {
  const t = useT();
  const serverKey = useServerKey();
  if (agent.server_key_fingerprint === "") {
    return <KeyValue label={t("action.server_key")} value={t("action.server_key_none")} />;
  }
  const differs = serverKey.data !== null && serverKey.data.fingerprint !== agent.server_key_fingerprint;
  return (
    <>
      <KeyValue label={t("action.server_key")} value={agent.server_key_fingerprint} mono />
      {differs && serverKey.data && <Note tone="danger">{t("action.server_key_differs", serverKey.data.fingerprint)}</Note>}
    </>
  );
}

function RunRow({ run, now }: { run: ActionRun; now: number }) {
  const t = useT();
  const output = run.output.map((line) => line.line).join("\n");
  return (
    <tr>
      <td>{formatWhen(run.requested_at, now)}</td>
      <td>{t(`action.kind_${run.kind.replace(".", "_")}`)}</td>
      <td>
        <Pill tone={stateTones[run.state]}>{t(`action.state_${run.state}`)}</Pill>
        {run.reason !== "" && <div className="secondary">{t(`action.reason_${run.reason.split(":")[0]}`)}</div>}
        {output !== "" && <pre className="secondary">{output}</pre>}
      </td>
      <td className="wide-only">{run.requested_by}</td>
    </tr>
  );
}
