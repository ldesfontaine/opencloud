import { useMemo } from "react";
import { Outlet } from "react-router";

import { deleteMachineToken, useMachines, type PendingToken } from "../api/machines";
import { useCurrentResources } from "../api/resources";
import { ActionButton } from "../components/ActionButton";
import { Button } from "../components/Button";
import { Card, CardHeader } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Dot, Pill } from "../components/Pill";
import { List, RowText, Table } from "../components/Table";
import { Verdict } from "../components/Verdict";
import { useNow } from "../hooks/useNow";
import { useAction } from "../hooks/useAction";
import { useT, type Translate } from "../i18n/context";
import { buildMachineTree, summarizeMachines, type TreeSummary } from "../lib/machineTree";
import { liveMachines, machinesSubtitle } from "../lib/machines";
import { formatRemaining } from "../lib/time";
import { MachineTreeRows } from "./machines/MachineTree";

export function Machines() {
  const t = useT();
  const now = useNow();
  const machines = useMachines();
  const resources = useCurrentResources();
  const list = machines.data?.machines ?? [];
  const live = liveMachines(list);
  const online = live.filter((machine) => machine.online).length;
  const rows = useMemo(() => buildMachineTree(list), [list]);
  const currents = useMemo(() => new Map((resources.data?.machines ?? []).map((current) => [current.machine_id, current])), [resources.data]);
  return (
    <>
      <PageHead
        title={t("nav.machines")}
        subtitle={machines.data ? machinesSubtitle(t, live.length, online) : ""}
        actions={
          <ActionButton route="POST /api/machines/tokens" icon="plus" to="/machines/nouvelle">
            {t("machine.add")}
          </ActionButton>
        }
      />
      {machines.error && <Failure error={machines.error} />}
      {machines.data && (
        <>
          {list.length > 0 && <MachinesVerdict summary={summarizeMachines(list, rows)} />}
          <Card>
            <Table>
              <thead>
                <tr>
                  <th>{t("machines.col_machine")}</th>
                  <th className="wide-only">{t("machines.col_address")}</th>
                  <th className="wide-only">{t("machines.col_resources")}</th>
                  <th>{t("machines.col_state")}</th>
                  <th>{t("machines.col_seen")}</th>
                  <th className="wide-only">{t("machines.col_agent")}</th>
                  <th className="th-end wide-only" />
                </tr>
              </thead>
              <tbody>
                <MachineTreeRows rows={rows} resources={currents} now={now} />
              </tbody>
            </Table>
          </Card>
          {machines.data.pending.length > 0 && <Pending tokens={machines.data.pending} now={now} onChange={machines.reload} />}
        </>
      )}
      {/* La fenêtre de l'adresse : « Ajouter une machine », ou le jeton d'un ré-enrôlement. */}
      <Outlet />
    </>
  );
}

// Est-ce que ça va ? Une machine qui ne répond pas teinte le verdict ; des
// invités que nul agent ne suit se disent, sans l'alarmer : ce n'est pas une panne.
function MachinesVerdict({ summary }: { summary: TreeSummary }) {
  const t = useT();
  const hasMissing = summary.missing.length > 0;
  const names = summary.missing.map((machine) => machine.name).join(", ");
  return (
    <Verdict
      tone={hasMissing ? "danger" : "ok"}
      pill={<Pill tone={hasMissing ? "danger" : "ok"}>{t(hasMissing ? "machines.verdict_down" : "machines.verdict_ok")}</Pill>}
      fact={hasMissing ? missingFact(t, summary.missing.length, names) : t("machines.verdict_all", summary.total)}
      context={summary.guestsWithoutAgent === 0 ? null : summary.guestsWithoutAgent === 1 ? t("machines.verdict_guests_one") : t("machines.verdict_guests", summary.guestsWithoutAgent)}
    >
      {t("machines.verdict_counts", summary.total, summary.online)}
    </Verdict>
  );
}

function missingFact(t: Translate, count: number, names: string): string {
  return count === 1 ? t("machines.verdict_missing_one", names) : t("machines.verdict_missing", count, names);
}

// Les jetons émis dont la commande n'a pas encore été jouée.
function Pending({ tokens, now, onChange }: { tokens: PendingToken[]; now: number; onChange: () => void }) {
  const t = useT();
  const action = useAction();
  const cancel = (id: string) => {
    action.run(() => deleteMachineToken(id), { topic: "all", onDone: onChange });
  };
  return (
    <Card>
      <CardHeader title={t("machines.pending_title")} aside={t("machines.pending_text")} />
      <Failure error={action.error} />
      <List>
        {tokens.map((token) => (
          <div className="row" key={token.id}>
            <Dot tone="neutral" />
            <RowText title={token.name}>
              <span className="mono">{token.masked}</span> · {t("machines.pending_expires", formatRemaining(t, Date.parse(token.expires_at) - now))}
              {token.reenroll && <> · {t("machines.pending_reenroll")}</>}
            </RowText>
            <span className="when">
              <Button variant="ghost" small onClick={() => cancel(token.id)}>
                {t("machines.cancel_token")}
              </Button>
            </span>
          </div>
        ))}
      </List>
    </Card>
  );
}
