import { Link } from "react-router";

import type { Counts, MachinesResponse, ResourcesResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, Empty, Stat } from "../components/Card";
import { Failure } from "../components/Failure";
import { NetworkOverview } from "../components/NetworkOverview";
import { PageHead } from "../components/PageHead";
import { StatePill } from "../components/Pill";
import { MachineMeters } from "../components/Resources";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useCertificateThresholds, useT, type Translate } from "../i18n/context";
import { daysUntil } from "../lib/certificates";
import { machinesSubtitle } from "../lib/subtitles";
import { seenAgo } from "../lib/time";
import "./Overview.scss";

// Le pied de la carte Domaines dit une chose à la fois, la plus pressante :
// une sonde hors ligne d'abord, puis un certificat qui approche de sa fin,
// puis que tout va bien. Un seul fait, celui qui appelle une action.
function DomainsFoot({ counts }: { counts: Counts }) {
  const t = useT();
  const now = useNow();
  const thresholds = useCertificateThresholds();
  if (counts.probes.attention > 0) {
    return <b className="danger">{t("overview.probes_attention", counts.probes.attention)}</b>;
  }
  const soonest = counts.certificates.soonest_expires_at;
  if (counts.certificates.expiring > 0 && soonest !== null) {
    const days = daysUntil(soonest, now);
    const tone = days < 0 || days <= thresholds.danger ? "danger" : "warn";
    return <b className={tone}>{certificateFoot(t, counts.certificates.expiring, days)}</b>;
  }
  return <>{t("overview.probes_ok")}</>;
}

// « 1 expire dans 12 jours », ou combien sont concernés quand il y en a
// plusieurs ; expiré, c'est déjà fait, et cela se dit autrement.
function certificateFoot(t: Translate, expiring: number, days: number): string {
  if (days < 0) {
    return expiring === 1 ? t("overview.certificate_expired") : t("overview.certificates_expired", expiring);
  }
  return expiring === 1 ? t("overview.certificate_expiring", days) : t("overview.certificates_expiring", expiring, days);
}

// La vue d'ensemble compte les machines, les services, les sondes et les
// tâches, dessine le réseau de
// toutes les machines, puis montre chaque machine en carte avec ses trois
// jauges ; sans machine, elle invite à en ajouter une.
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
              icon="box"
              label={t("nav.services")}
              value={counts.data?.services.total ?? "–"}
              foot={
                counts.data && counts.data.services.attention > 0 ? (
                  <b className="danger">{t("overview.services_attention", counts.data.services.attention)}</b>
                ) : (
                  t("overview.services_ok")
                )
              }
            />
            <Stat
              icon="globe"
              label={t("nav.domains")}
              value={counts.data?.probes.total ?? "–"}
              foot={counts.data ? <DomainsFoot counts={counts.data} /> : "–"}
            />
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
              <span className="card-t">{t("tab.network")}</span>
              {list.length === 1 && list[0] !== undefined && <Link to={`/machines/${list[0].id}/reseau`}>{t("network.overview_detail")}</Link>}
            </div>
            <NetworkOverview />
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
