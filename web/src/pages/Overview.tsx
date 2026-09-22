import { Link } from "react-router";

import type { AlertsResponse, Counts, MachinesResponse, ResourcesResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, Empty, Stat } from "../components/Card";
import { List } from "../components/Table";
import { Failure } from "../components/Failure";
import { NetworkOverview } from "../components/NetworkOverview";
import { PageHead } from "../components/PageHead";
import { Pill, StatePill } from "../components/Pill";
import { MachineMeters } from "../components/Resources";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useCertificateThresholds, useT } from "../i18n/context";
import { daysUntil } from "../lib/certificates";
import { machinesSubtitle } from "../lib/subtitles";
import { seenAgo } from "../lib/time";
import { AlertRow } from "./Alerts";
import "./Overview.scss";

const alertsShown = 3;

// La carte Alertes : le compte des ouvertes en pastille, les trois
// premières avec leur fait et leur cause, le reste par le lien.
function AlertsCard() {
  const t = useT();
  const alerts = useResource<AlertsResponse>("/api/alerts");
  if (!alerts.data) {
    return null;
  }
  const open = alerts.data.counts.open;
  return (
    <div className="overview-machines overview-alerts">
      <div className="overview-head">
        <span className="cluster">
          <span className="card-t">{t("alerts.title")}</span>
          {open > 0 ? (
            <Pill tone={alerts.data.counts.unacknowledged > 0 ? "danger" : "accent"} dot={false}>
              {t("alerts.overview_open", open)}
            </Pill>
          ) : (
            <Pill tone="ok" dot={false}>
              {t("alerts.overview_none")}
            </Pill>
          )}
        </span>
        <Link to="/alertes">{t("alerts.overview_see_all")}</Link>
      </div>
      {open > 0 && (
        <Card>
          <List>
            {alerts.data.alerts.slice(0, alertsShown).map((alert) => (
              <AlertRow key={alert.id} alert={alert} compact />
            ))}
          </List>
        </Card>
      )}
    </div>
  );
}

// Le pied de la carte Domaines dit une chose à la fois, la plus pressante :
// une sonde hors ligne, puis un certificat déjà expiré, puis un qui
// approche de sa fin, puis que tout va bien. Un seul fait, celui qui
// appelle une action.
function DomainsFoot({ counts }: { counts: Counts }) {
  const t = useT();
  const now = useNow();
  const thresholds = useCertificateThresholds();
  const certificates = counts.certificates;
  if (counts.probes.attention > 0) {
    return <b className="danger">{t("overview.probes_attention", counts.probes.attention)}</b>;
  }
  if (certificates.expired > 0) {
    const key = certificates.expired === 1 ? "overview.certificate_expired" : "overview.certificates_expired";
    return <b className="danger">{t(key, certificates.expired)}</b>;
  }
  const soonest = certificates.soonest_expires_at;
  if (certificates.expiring > 0 && soonest !== null) {
    const days = daysUntil(soonest, now);
    const key = certificates.expiring === 1 ? "overview.certificate_expiring" : "overview.certificates_expiring";
    return (
      <b className={days <= thresholds.danger ? "danger" : "warn"}>
        {certificates.expiring === 1 ? t(key, days) : t(key, certificates.expiring, days)}
      </b>
    );
  }
  return <>{t("overview.probes_ok")}</>;
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
                ) : counts.data && counts.data.services.updates > 0 ? (
                  <b className="accent">
                    {counts.data.services.updates === 1 ? t("overview.service_update") : t("overview.services_updates", counts.data.services.updates)}
                  </b>
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
          <AlertsCard />
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
