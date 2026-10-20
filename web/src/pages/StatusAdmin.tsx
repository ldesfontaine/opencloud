import { useEffect, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router";

import { errorKey, type ApiError } from "../api/client";
import {
  saveStatusPageSettings,
  useIncidents,
  useStatusComponents,
  useStatusPageSettings,
  type Incident,
  type StatusComponent,
  type StatusComponentsResponse,
  type StatusState,
} from "../api/status";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, EmptyCard } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { Icon } from "../components/Icon";
import { PageHead } from "../components/PageHead";
import { TabNav } from "../components/TabNav";
import { Pill } from "../components/Pill";
import { Subject, Table } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useI18n, useT } from "../i18n/context";
import { impactTones, incidentTones, incidentsSubtitle, isOpen, statusStateTones } from "../lib/status";
import { ago } from "../lib/time";
import { NotFound } from "./NotFound";
import "./StatusAdmin.scss";

const tabs = [
  { slug: "", key: "statuspage.tab_components" },
  { slug: "incidents", key: "statuspage.tab_incidents" },
  { slug: "reglages", key: "statuspage.tab_settings" },
];

// La pastille d'un état de la page de statut ; vide : rien à dire.
export function StatePill({ state }: { state: StatusState }) {
  const t = useT();
  return <Pill tone={statusStateTones[state]}>{state === "" ? "—" : t(`status.state_${state}`)}</Pill>;
}

// L'administration de la page de statut : ses composants, ses incidents,
// ses réglages, sous trois onglets. Ce que le public ne voit jamais se lit
// ici : les objets rattachés, leur état, ce qui est caché.
export function StatusAdmin() {
  const { tab = "" } = useParams();
  const t = useT();
  const components = useStatusComponents();
  if (!tabs.some((candidate) => candidate.slug === tab)) {
    return <NotFound />;
  }
  const publicURL = components.data?.public_url ?? "";
  return (
    <>
      <PageHead
        title={t("statuspage.title")}
        subtitle={publicURL !== "" ? t("statuspage.subtitle", publicURL) : ""}
        actions={
          <>
            {publicURL !== "" && (
              <a className="btn btn-secondary" href={publicURL} target="_blank" rel="noopener noreferrer">
                <Icon name="external" />
                {t("statuspage.open_public")}
              </a>
            )}
            {tab === "" && (
              <Button variant="primary" icon="plus" to="/page-statut/composants/nouveau">
                {t("statuspage.add_component")}
              </Button>
            )}
            {tab === "incidents" && (
              <Button variant="primary" icon="plus" to="/page-statut/incidents/nouveau">
                {t("statuspage.add_incident")}
              </Button>
            )}
          </>
        }
      />
      <TabNav tabs={tabs.map((entry) => ({ to: entry.slug === "" ? "/page-statut" : `/page-statut/${entry.slug}`, key: entry.key }))} />
      {tab === "" && <ComponentsTab response={components.data} error={components.error} />}
      {tab === "incidents" && <IncidentsTab />}
      {tab === "reglages" && <SettingsTab />}
    </>
  );
}

function ComponentsTab({ response, error }: { response: StatusComponentsResponse | null; error: ApiError | null }) {
  const t = useT();
  if (error) {
    return <Failure error={error} />;
  }
  if (!response) {
    return null;
  }
  return (
    <>
      <Card>
        <CardBody>
          <span className="machine-state">
            <span className="field-label">{t("statuspage.global_label")}</span>
            {response.global === "" ? <Pill tone="neutral">{t("statuspage.global_none")}</Pill> : <StatePill state={response.global} />}
          </span>
        </CardBody>
      </Card>
      {response.components.length === 0 ? (
        <EmptyCard icon="eye" title={t("statuspage.components_empty_title")} text={t("statuspage.components_empty_text")} />
      ) : (
        <Card className="scroll-x">
          <Table>
            <thead>
              <tr>
                <th>{t("statuspage.col_component")}</th>
                <th className="wide-only">{t("statuspage.col_objects")}</th>
                <th className="wide-only">{t("statuspage.col_derived")}</th>
                <th className="th-end">{t("statuspage.col_effective")}</th>
              </tr>
            </thead>
            <tbody>
              {response.components.map((component) => (
                <ComponentRow key={component.id} component={component} />
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </>
  );
}

function ComponentRow({ component }: { component: StatusComponent }) {
  const t = useT();
  const names = component.members.map((member) => member.name || t("component.missing")).join(", ");
  const imposed = component.effective !== component.derived;
  return (
    <tr>
      <td>
        <Link className="svc" to={`/page-statut/composants/${component.id}`}>
          <Subject icon="eye">{component.name}</Subject>
        </Link>
      </td>
      <td className="wide-only">
        <span className="cell-stack">
          <span>{t(component.members.length === 1 ? "statuspage.objects_count" : "statuspage.objects_counts", component.members.length)}</span>
          {names !== "" && <span className="secondary">{names}</span>}
        </span>
      </td>
      <td className="wide-only">
        <StatePill state={component.derived} />
      </td>
      <td className="td-end">
        <span className="cell-stack end">
          <StatePill state={component.effective} />
          {component.effective === "" && <span className="secondary">{t("statuspage.hidden")}</span>}
          {imposed && component.effective !== "" && <span className="secondary">{t("statuspage.imposed")}</span>}
        </span>
      </td>
    </tr>
  );
}

function IncidentsTab() {
  const t = useT();
  const now = useNow();
  const incidents = useIncidents();
  const list = incidents.data?.incidents ?? [];
  const open = list.filter(isOpen).length;
  if (incidents.error) {
    return <Failure error={incidents.error} />;
  }
  if (!incidents.data) {
    return null;
  }
  if (list.length === 0) {
    return (
      <EmptyCard icon="bell" title={t("incident.empty_title")} text={t("incident.empty_text")} />
    );
  }
  return (
    <Card className="scroll-x">
      <CardHeader title={t("statuspage.tab_incidents")} aside={incidentsSubtitle(t, list.length, open)} />
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
          {list.map((incident) => (
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

// Le titre, l'annonce et la langue de la page : trois champs, un bouton.
function SettingsTab() {
  const t = useT();
  const { languages } = useI18n();
  const settings = useStatusPageSettings();
  const [title, setTitle] = useState("");
  const [announcement, setAnnouncement] = useState("");
  const [language, setLanguage] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  // Les réglages se lisent une fois : une relecture (le signal de
  // rafraîchissement passe) ne doit pas écraser ce que l'opérateur est en
  // train de taper.
  const [filled, setFilled] = useState(false);

  useEffect(() => {
    if (filled || settings.data === null) {
      return;
    }
    setFilled(true);
    setTitle(settings.data.title);
    setAnnouncement(settings.data.announcement);
    setLanguage(settings.data.language);
  }, [settings.data, filled]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setSaved(false);
    saveStatusPageSettings({ title, announcement, language })
      .then((kept) => {
        // Le formulaire reprend les réglages gardés, tels que le serveur les
        // a rendus.
        setTitle(kept.title);
        setAnnouncement(kept.announcement);
        setLanguage(kept.language);
        setError(null);
        setSaved(true);
        settings.reload();
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  if (settings.error) {
    return <Failure error={settings.error} />;
  }
  if (!settings.data) {
    return null;
  }
  return (
    <Card className="form-card">
      <CardHeader title={t("statuspage.settings_title")} aside={t("statuspage.settings_text")} />
      <form className="card-b" onSubmit={submit}>
        <Field label={t("statuspage.title_label")} htmlFor="status-title" help={t("statuspage.title_help")}>
          <Input id="status-title" type="text" value={title} onChange={(event) => setTitle(event.target.value)} maxLength={120} autoComplete="off" />
        </Field>
        <Field label={t("statuspage.announcement_label")} htmlFor="status-announcement" help={t("statuspage.announcement_help")}>
          <textarea id="status-announcement" className="input textarea" value={announcement} onChange={(event) => setAnnouncement(event.target.value)} maxLength={500} rows={3} />
        </Field>
        <Field label={t("statuspage.language_label")} htmlFor="status-language" help={t("statuspage.language_help")}>
          <Select id="status-language" value={language} onChange={(event) => setLanguage(event.target.value)}>
            {languages.map((code) => (
              <option key={code} value={code}>
                {t(`language.${code}`)}
              </option>
            ))}
          </Select>
        </Field>
        <Failure error={error} />
        <div className="cluster">
          <Button type="submit" variant="primary" icon="check" disabled={busy}>
            {t("statuspage.save")}
          </Button>
          {saved && <Pill tone="ok">{t("statuspage.saved")}</Pill>}
        </div>
      </form>
    </Card>
  );
}
