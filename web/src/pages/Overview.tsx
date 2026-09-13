import { Link } from "react-router";

import type { Counts, MachinesResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardHeader, Empty, Stat } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Dot } from "../components/Pill";
import { List, RowText, RowWhen } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { machinesSubtitle } from "../lib/subtitles";
import { seenAgo } from "../lib/time";

// La vue d'ensemble compte les machines et les tâches ; sans machine, elle
// invite à en ajouter une.
export function Overview() {
  const t = useT();
  const now = useNow();
  const machines = useResource<MachinesResponse>("/api/machines");
  const counts = useResource<Counts>("/api/counts");
  const list = machines.data?.machines ?? [];
  const online = list.filter((machine) => machine.online).length;
  return (
    <>
      <PageHead
        title={t("nav.overview")}
        subtitle={machines.data ? machinesSubtitle(t, list.length, online) : ""}
        actions={
          <Button variant="primary" icon="plus" to="/machines/nouvelle">
            {t("machine.add")}
          </Button>
        }
      />
      {machines.error && <Failure error={machines.error} />}
      {machines.data && list.length === 0 && (
        <Card>
          <Empty icon="server" title={t("overview.empty_title")} text={t("overview.empty_text")} />
        </Card>
      )}
      {machines.data && list.length > 0 && (
        <>
          <div className="grid-4">
            <Stat icon="server" label={t("nav.machines")} value={list.length} foot={<b>{t("overview.online", online)}</b>} />
            <Stat
              icon="clock"
              label={t("nav.jobs")}
              value={counts.data?.jobs.total ?? "–"}
              foot={
                counts.data && counts.data.jobs.attention > 0 ? (
                  <b className="danger">{t("overview.jobs_attention", counts.data.jobs.attention)}</b>
                ) : (
                  t("overview.jobs_ok")
                )
              }
            />
          </div>
          <Card>
            <CardHeader title={t("nav.machines")} aside={<Link to="/machines">{t("overview.see_all")}</Link>} />
            <List>
              {list.map((machine) => (
                <Link className="row" key={machine.id} to={`/machines/${machine.id}`}>
                  <Dot tone={machine.online ? "ok" : "danger"} />
                  <RowText title={machine.name}>
                    {machine.address !== "" && (
                      <>
                        <span className="mono">{machine.address}</span> ·{" "}
                      </>
                    )}
                    {machine.os}
                  </RowText>
                  <RowWhen>{seenAgo(t, machine.last_seen_at, now)}</RowWhen>
                </Link>
              ))}
            </List>
          </Card>
        </>
      )}
    </>
  );
}
