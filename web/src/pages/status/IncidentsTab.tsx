import { Link } from "react-router";

import { useIncidents, type Incident } from "../../api/status";
import { Card, CardHeader, EmptyCard } from "../../components/Card";
import { Loaded } from "../../components/Loaded";
import { Pill } from "../../components/Pill";
import { Subject, Table } from "../../components/Table";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { impactTones, incidentTones, incidentsSubtitle, isOpen } from "../../lib/status";
import { ago } from "../../lib/time";

export function IncidentsTab() {
  const incidents = useIncidents();
  return <Loaded resource={incidents}>{(data) => <IncidentList incidents={data.incidents} />}</Loaded>;
}

function IncidentList({ incidents }: { incidents: Incident[] }) {
  const t = useT();
  const now = useNow();
  const open = incidents.filter(isOpen).length;
  if (incidents.length === 0) {
    return <EmptyCard icon="bell" title={t("incident.empty_title")} text={t("incident.empty_text")} />;
  }
  return (
    <Card className="scroll-x">
      <CardHeader title={t("statuspage.tab_incidents")} aside={incidentsSubtitle(t, incidents.length, open)} />
      <Table>
        <thead>
          <tr>
            <th>{t("incident.col_incident")}</th>
            <th className="wide-only">{t("incident.col_impact")}</th>
            <th>{t("incident.col_status")}</th>
            <th className="wide-only">{t("incident.col_components")}</th>
            <th className="th-end wide-only">{t("incident.col_updated")}</th>
          </tr>
        </thead>
        <tbody>
          {incidents.map((incident) => (
            <IncidentRow key={incident.id} incident={incident} now={now} />
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function IncidentRow({ incident, now }: { incident: Incident; now: number }) {
  const t = useT();
  return (
    <tr>
      <td>
        <Link className="svc" to={`/page-statut/incidents/${incident.id}`}>
          <Subject icon={incident.impact === "maintenance" ? "clock" : "bell"}>{incident.title}</Subject>
        </Link>
      </td>
      <td className="wide-only">
        <Pill tone={impactTones[incident.impact]}>{t(`incident.impact_${incident.impact}`)}</Pill>
      </td>
      <td>
        <Pill tone={incidentTones[incident.status]}>{t(`incident.status_${incident.status}`)}</Pill>
      </td>
      <td className="secondary wide-only">{incident.components.map((component) => component.name).join(", ")}</td>
      <td className="secondary nowrap td-end wide-only">{ago(t, incident.updated_at, now)}</td>
    </tr>
  );
}
