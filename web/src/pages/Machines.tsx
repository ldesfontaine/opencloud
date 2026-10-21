import { Link } from "react-router";

import { deleteMachineToken, useMachines, type PendingToken } from "../api/machines";
import { Button } from "../components/Button";
import { Card, CardHeader } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Dot, Pill, OnlinePill } from "../components/Pill";
import { List, RowText, Subject, Table } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { machineSeen, machinesSubtitle } from "../lib/machines";
import { formatRemaining } from "../lib/time";

export function Machines() {
  const t = useT();
  const now = useNow();
  const machines = useMachines();
  const list = machines.data?.machines ?? [];
  const online = list.filter((machine) => machine.online).length;
  return (
    <>
      <PageHead
        title={t("nav.machines")}
        subtitle={machines.data ? machinesSubtitle(t, list.length, online) : ""}
        actions={
          <Button variant="primary" icon="plus" to="/machines/nouvelle">
            {t("machine.add")}
          </Button>
        }
      />
      {machines.error && <Failure error={machines.error} />}
      {machines.data && (
        <>
          <Card>
            <Table>
              <thead>
                <tr>
                  <th>{t("machines.col_machine")}</th>
                  <th className="wide-only">{t("machines.col_address")}</th>
                  <th className="wide-only">{t("machines.col_os")}</th>
                  <th className="wide-only">{t("machines.col_agent")}</th>
                  <th>{t("machines.col_state")}</th>
                  <th>{t("machines.col_seen")}</th>
                  <th className="th-end wide-only" />
                </tr>
              </thead>
              <tbody>
                {list.map((machine) => (
                  <tr key={machine.id}>
                    <td>
                      <Link className="svc" to={`/machines/${machine.id}`}>
                        <Subject icon="server">
                          {machine.name}
                          {machine.local && (
                            <Pill tone="neutral" dot={false}>
                              {t("machine.this_machine")}
                            </Pill>
                          )}
                        </Subject>
                      </Link>
                    </td>
                    <td className="num wide-only">{machine.address}</td>
                    <td className="wide-only">{machine.os}</td>
                    <td className="num wide-only">{machine.agent?.version ?? "—"}</td>
                    <td>
                      <OnlinePill online={machine.online} />
                    </td>
                    <td className="secondary nowrap">{machineSeen(t, machine, now)}</td>
                    <td className="td-end wide-only">
                      <Button variant="ghost" small to={`/machines/${machine.id}`}>
                        {t("machines.open")}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </Card>
          {machines.data.pending.length > 0 && <Pending tokens={machines.data.pending} now={now} onChange={machines.reload} />}
        </>
      )}
    </>
  );
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
