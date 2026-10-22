import type { ReactNode } from "react";
import { Link } from "react-router";

import { useAlerts, type AlertsResponse } from "../api/alerts";
import { useCounts, type Counts } from "../api/counts";
import { useMachines } from "../api/machines";
import { useCurrentResources } from "../api/resources";
import { Button } from "../components/Button";
import { EmptyCard, Stat } from "../components/Card";
import { Failure } from "../components/Failure";
import { NetworkOverview } from "../components/NetworkOverview";
import { PageHead } from "../components/PageHead";
import { OnlinePill } from "../components/Pill";
import { MachineMeters } from "../components/Resources";
import { useNow } from "../hooks/useNow";
import { useCertificateThresholds, useDiskThresholds, useT } from "../i18n/context";
import { describeAlert, mostUrgentAlert } from "../lib/alerts";
import { daysUntil } from "../lib/certificates";
import { machineSeen, machinesSubtitle } from "../lib/machines";
import "./Overview.scss";

// La tuile Alertes : le nombre d'ouvertes, puis en pied, sur une ligne
// tronquée, ce qui reste à acquitter et la plus urgente (son fait et sa
// machine ; le texte entier est en infobulle). La liste des alertes ne tient
// plus sur cette page (#141) : la tuile mène à elle.
function AlertsTile({ alerts }: { alerts: AlertsResponse | null }) {
  const t = useT();
  const now = useNow();
  const disks = useDiskThresholds();
  const counts = alerts?.counts;
  let foot: ReactNode = "–";
  if (alerts && counts) {
    if (counts.open === 0) {
      foot = t("alerts.overview_none");
    } else {
      const lead = counts.unacknowledged > 0 ? t("overview.alerts_unacknowledged", counts.unacknowledged) : t("overview.alerts_acknowledged");
      const urgent = mostUrgentAlert(alerts.alerts);
      const urgentText = urgent === null ? "" : t("overview.alerts_top", describeAlert(t, urgent, now, disks).fact, urgent.machine_name !== "" ? urgent.machine_name : urgent.object.name);
      foot = (
        <span title={urgentText === "" ? lead : `${lead} · ${urgentText}`}>
          {counts.unacknowledged > 0 ? <b className="danger">{lead}</b> : lead}
          {urgentText !== "" && ` · ${urgentText}`}
        </span>
      );
    }
  }
  return (
    <Link className="stat-link" to="/alertes">
      <Stat compact icon="bell" label={t("alerts.title")} value={counts?.open ?? "–"} foot={foot} />
    </Link>
  );
}

// Le pied de la carte Disponibilité dit une chose à la fois, la plus pressante :
// une sonde hors ligne, puis un certificat déjà expiré, puis un qui
// approche de sa fin, puis que tout va bien. Un seul fait, celui qui
// appelle une action.
function AvailabilityFoot({ counts }: { counts: Counts }) {
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

// La vue d'ensemble tient sur un écran de portable : une bande de cinq
// tuiles (machines, conteneurs, disponibilité, tâches, alertes dont la plus
// urgente), le réseau de toutes les machines mis à l'échelle,
// puis une rangée de cartes de machine serrées avec leurs trois jauges ;
// sans machine, elle invite à en ajouter une.
export function Overview() {
  const t = useT();
  const now = useNow();
  const machines = useMachines();
  const counts = useCounts();
  const alerts = useAlerts();
  const resources = useCurrentResources();
  const list = machines.data?.machines ?? [];
  const online = list.filter((machine) => machine.online).length;
  const currentOf = (machineID: string) => resources.data?.machines.find((current) => current.machine_id === machineID);
  return (
    <div className="overview-page">
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
        <EmptyCard icon="server" title={t("overview.empty_title")} text={t("overview.empty_text")} />
      )}
      {machines.data && list.length > 0 && (
        <>
          <div className="overview-stats">
            <Stat compact icon="server" label={t("nav.machines")} value={list.length} foot={<b>{t("overview.online", online)}</b>} />
            <Stat
              compact
              icon="box"
              label={t("services.tab_containers")}
              value={counts.data?.containers.total ?? "–"}
              foot={
                counts.data && counts.data.containers.attention > 0 ? (
                  <b className="danger">{t("overview.services_attention", counts.data.containers.attention)}</b>
                ) : counts.data && counts.data.containers.updates > 0 ? (
                  <b className="accent">
                    {counts.data.containers.updates === 1 ? t("overview.service_update") : t("overview.services_updates", counts.data.containers.updates)}
                  </b>
                ) : (
                  t("overview.services_ok")
                )
              }
            />
            <Stat
              compact
              icon="globe"
              label={t("nav.availability")}
              value={counts.data?.probes.total ?? "–"}
              foot={counts.data ? <AvailabilityFoot counts={counts.data} /> : "–"}
            />
            <Stat
              compact
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
            <AlertsTile alerts={alerts.data} />
          </div>
          <div className="overview-block">
            <div className="overview-head">
              <span className="card-t">{t("tab.network")}</span>
              {list.length === 1 && list[0] !== undefined && <Link to={`/machines/${list[0].id}/reseau`}>{t("network.overview_detail")}</Link>}
            </div>
            <NetworkOverview />
          </div>
          <div className="overview-block">
            <div className="overview-head">
              <span className="card-t">{t("nav.machines")}</span>
              <Link to="/machines">{t("overview.see_all")}</Link>
            </div>
            <div className="overview-machines-grid">
              {list.map((machine) => (
                <Link className="card machine-card" key={machine.id} to={`/machines/${machine.id}`}>
                  <div className="machine-card-head">
                    <span className="machine-card-title">
                      <span className="card-t">{machine.name}</span>
                      <span className="mono muted" title={machine.address !== "" ? machine.address : machine.os}>
                        {machine.address !== "" ? machine.address : machine.os}
                      </span>
                    </span>
                    <OnlinePill online={machine.online} />
                  </div>
                  <MachineMeters current={currentOf(machine.id)} />
                  <div className="machine-card-foot secondary">
                    <span>{machine.os}</span>
                    <span>{machineSeen(t, machine, now)}</span>
                  </div>
                </Link>
              ))}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
