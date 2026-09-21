import { useCallback, useEffect, useMemo, useState } from "react";

import type { PublicComponent, PublicIncident, PublicStatus, StatusDay } from "../api/types";
import { Mark } from "../components/Icon";
import { useNow } from "../hooks/useNow";
import { format } from "../i18n/format";
import { dayKeys, dayTone } from "../lib/probes";
import { formatPercent, incidentTones, isMaintenance, stateTones, uptimeOf } from "../lib/status";

type Catalog = Record<string, string>;
type Translate = (key: string, ...args: (string | number)[]) => string;

const statusURL = "/statut/api/status";
const catalogURL = "/statut/api/i18n";
const eventsURL = "/statut/api/events";
const uptimeSpan = 90;

// La page de statut telle qu'un visiteur la lit : le serveur rend des
// faits et les chaînes de la langue réglée ; tout le reste se fait ici.
// Le direct public ne dit que « status » : on relit.
export function StatusPage() {
  const [data, setData] = useState<PublicStatus | null>(null);
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [failed, setFailed] = useState(false);

  const load = useCallback(async () => {
    const response = await fetch(statusURL, { credentials: "same-origin" });
    if (!response.ok) {
      throw new Error(String(response.status));
    }
    setData((await response.json()) as PublicStatus);
    setFailed(false);
  }, []);

  useEffect(() => {
    fetch(catalogURL, { credentials: "same-origin" })
      .then((response) => (response.ok ? (response.json() as Promise<Catalog>) : Promise.reject(new Error(String(response.status)))))
      .then(setCatalog)
      .catch(() => setFailed(true));
    load().catch(() => setFailed(true));
  }, [load]);

  useEffect(() => {
    const source = new EventSource(eventsURL);
    const reload = () => {
      load().catch(() => setFailed(true));
    };
    source.addEventListener("status", reload);
    source.addEventListener("reconnected", reload);
    return () => source.close();
  }, [load]);

  const t = useMemo<Translate>(
    () => (key, ...args) => {
      const template = catalog?.[key];
      if (template === undefined) {
        return `[${key}]`;
      }
      return args.length > 0 ? format(template, args) : template;
    },
    [catalog],
  );

  useEffect(() => {
    if (data === null || catalog === null) {
      return;
    }
    document.documentElement.lang = data.language;
    document.title = data.title !== "" ? data.title : t("status.default_title");
  }, [data, catalog, t]);

  if (failed) {
    // Sans catalogue, pas de traduction : les deux langues d'un coup.
    return <p className="boot-error">La page ne répond pas. · The page is not responding.</p>;
  }
  if (data === null || catalog === null) {
    return null;
  }
  return <StatusView data={data} t={t} />;
}

function StatusView({ data, t }: { data: PublicStatus; t: Translate }) {
  const now = useNow();
  const current = data.open.filter((incident) => !isMaintenance(incident));
  const planned = data.open.filter((incident) => isMaintenance(incident));
  const global = data.global === "" ? "unknown" : data.global;
  return (
    <main className="status-page">
      <header className="status-top">
        <span className="wordmark">
          <span className="mark">
            <Mark />
          </span>
          <span>open</span>
          <b>Cloud</b>
        </span>
        <span className="status-updated">{updatedAgo(t, data.generated_at, now)}</span>
      </header>
      {data.announcement !== "" && <div className="status-announce">{data.announcement}</div>}
      <div className="status-head">
        <h1>{data.title !== "" ? data.title : t("status.default_title")}</h1>
      </div>
      <div className={`status-banner banner-${stateTones[data.global]}`}>
        <span className="banner-dot" />
        <div>
          <div className="banner-big">{t(`status.global_${global}`)}</div>
          <div className="banner-sub">{t(`status.global_${global}_sub`)}</div>
        </div>
      </div>
      <section className="card">
        <div className="card-h">
          <span className="card-t">{t("status.components")}</span>
        </div>
        {data.components.length === 0 ? (
          <p className="status-empty">{t("status.empty")}</p>
        ) : (
          <div className="status-rows">
            {data.components.map((component) => (
              <ComponentRow key={component.id} component={component} language={data.language} t={t} now={now} />
            ))}
          </div>
        )}
      </section>
      {current.length > 0 && <IncidentCard title={t("status.current")} incidents={current} language={data.language} t={t} />}
      {planned.length > 0 && <IncidentCard title={t("status.planned")} incidents={planned} language={data.language} t={t} />}
      {data.components.length > 0 && (
        <section className="card">
          <div className="card-h">
            <span className="card-t">{t("status.history")}</span>
          </div>
          {data.history.length === 0 ? (
            <p className="status-empty">{t("status.no_history")}</p>
          ) : (
            data.history.map((incident) => <IncidentBlock key={incident.id} incident={incident} language={data.language} t={t} />)
          )}
        </section>
      )}
      <footer className="status-foot">
        <span>{t("status.powered")}</span>
        <span>{t("status.local_times")}</span>
      </footer>
    </main>
  );
}

// Un composant : son nom, sa pastille, et sa barre des 90 derniers jours
// quand une sonde le mesure.
function ComponentRow({ component, language, t, now }: { component: PublicComponent; language: string; t: Translate; now: number }) {
  const uptime = uptimeOf(component.days);
  const percent = formatPercent(uptime.total, uptime.success, language);
  return (
    <div className="status-row">
      <span className="status-name">{component.name}</span>
      <span className={`pill pill-${stateTones[component.state]}`}>{t(`status.state_${component.state}`)}</span>
      {component.days.length > 0 && (
        <>
          <UptimeBar days={component.days} t={t} now={now} />
          <div className="status-barmeta">
            <span>{t("status.uptime_days")}</span>
            <span>{percent === null ? t("status.uptime_none") : t("status.uptime_pct", percent)}</span>
          </div>
        </>
      )}
    </div>
  );
}

function UptimeBar({ days, t, now }: { days: StatusDay[]; t: Translate; now: number }) {
  const byDay = new Map(days.map((day) => [day.day, day]));
  return (
    <div className="ubar status-bar" role="img" aria-label={t("status.uptime_days")}>
      {dayKeys(uptimeSpan, now).map((key) => {
        const day = byDay.get(key);
        const label = day === undefined || day.total === 0 ? t("status.day_nodata", key) : t("status.day", key, day.total);
        return <span className={`ubar-seg ubar-${dayTone(day)}`} key={key} title={label} />;
      })}
    </div>
  );
}

function IncidentCard({ title, incidents, language, t }: { title: string; incidents: PublicIncident[]; language: string; t: Translate }) {
  return (
    <section className="card">
      <div className="card-h">
        <span className="card-t">{title}</span>
      </div>
      {incidents.map((incident) => (
        <IncidentBlock key={incident.id} incident={incident} language={language} t={t} />
      ))}
    </section>
  );
}

// Un incident : son titre, son statut, ce qu'il touche, sa fenêtre s'il
// en a une, puis son fil du plus récent au plus ancien.
function IncidentBlock({ incident, language, t }: { incident: PublicIncident; language: string; t: Translate }) {
  return (
    <article className="status-incident">
      <div className="status-incident-head">
        <h3>{incident.title}</h3>
        <span className={`pill pill-${incidentTones[incident.status]}`}>{t(`status.incident_${incident.status}`)}</span>
      </div>
      {incident.starts_at !== null && incident.ends_at !== null && (
        <p className="status-window mono">{t("status.window", formatInstant(incident.starts_at, language), formatInstant(incident.ends_at, language))}</p>
      )}
      <p className="status-affects">
        {t("status.impact")} <span className={`impact-${stateTones[incident.impact]}`}>{t(`status.state_${incident.impact}`)}</span>
        {" · "}
        {t("status.affects")} <b>{incident.components.join(", ")}</b>
      </p>
      <ul className="status-timeline">
        {incident.updates.map((update, index) => (
          <li key={index}>
            <span className="tl-status">{t(`status.incident_${update.status}`)}</span>
            <span className="tl-when mono">{formatInstant(update.created_at, language)}</span>
            {update.message !== "" && <span className="tl-message">{update.message}</span>}
          </li>
        ))}
      </ul>
    </article>
  );
}

// Un instant en heure locale, dans la langue de la page.
function formatInstant(iso: string, language: string): string {
  return new Intl.DateTimeFormat(language, { dateStyle: "medium", timeStyle: "short" }).format(new Date(iso));
}

const second = 1000;
const minute = 60 * second;
const hour = 60 * minute;
const day = 24 * hour;

function updatedAgo(t: Translate, at: string, now: number): string {
  const elapsed = now - Date.parse(at);
  if (elapsed < second) {
    return t("status.updated_now");
  }
  if (elapsed < minute) {
    return t("status.updated_ago", t("status.seconds", Math.floor(elapsed / second)));
  }
  if (elapsed < hour) {
    return t("status.updated_ago", t("status.minutes", Math.floor(elapsed / minute)));
  }
  if (elapsed < day) {
    return t("status.updated_ago", t("status.hours", Math.floor(elapsed / hour)));
  }
  return t("status.updated_ago", t("status.days", Math.floor(elapsed / day)));
}
