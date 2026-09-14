import { Link } from "react-router";

import type { Counts, MachinesResponse, ResourcesResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, Empty, Stat } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { StatePill } from "../components/Pill";
import { MachineMeters } from "../components/Resources";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { machinesSubtitle } from "../lib/subtitles";
import { seenAgo } from "../lib/time";
import "./Overview.scss";

// La vue d'ensemble compte les machines et les tâches, puis montre chaque
// machine en carte avec ses trois jauges ; sans machine, elle invite à en
// ajouter une.
export function Overview() {
  const t = useT();
  const now = useNow();
  const machines = useResource<MachinesResponse>("/api/machines");
  const counts = useResource<Counts>("/api/counts");
  const resources = useResource<ResourcesResponse>("/api/resources");
  const list = machines.data?.machines ?? [];
  const online = list.filter((machine) => machine.online).length;
  const currentOf = (machineID: string) => resources.data?.machines.find((current) => current.machine_id === machineID);
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
          <div className="overview-machines">
            <div className="overview-head">
              <span className="card-t">{t("nav.machines")}</span>
              <Link to="/machines">{t("overview.see_all")}</Link>
            </div>
            <div className="grid-3">
              {list.map((machine) => (
                <Link className="card machine-card" key={machine.id} to={`/machines/${machine.id}`}>
                  <div className="card-h">
                    <span className="machine-card-title">
                      <span className="card-t">{machine.name}</span>
                      <span className="mono muted">{machine.address !== "" ? machine.address : machine.os}</span>
                    </span>
                    <StatePill online={machine.online} />
                  </div>
                  <div className="card-b">
                    <MachineMeters current={currentOf(machine.id)} />
                    <div className="kv">
                      <span className="k">{machine.os}</span>
                      <span className="secondary">{seenAgo(t, machine.last_seen_at, now)}</span>
                    </div>
                  </div>
                </Link>
              ))}
            </div>
          </div>
        </>
      )}
    </>
  );
}
