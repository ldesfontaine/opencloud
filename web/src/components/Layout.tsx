import { NavLink, Outlet } from "react-router";

import type { Counts } from "../api/types";
import { useLive } from "../hooks/useLive";
import { useResource } from "../hooks/useResource";
import { useI18n } from "../i18n/context";
import { Icon, Mark, type IconName } from "./Icon";
import "./Layout.scss";

export function Layout() {
  useLive();
  return (
    <div className="app">
      <Sidebar />
      <main className="main" id="main">
        <div className="content">
          <Outlet />
        </div>
      </main>
    </div>
  );
}

interface NavEntry {
  key: string;
  href: string;
  icon: IconName;
}

const viewEntries: NavEntry[] = [
  { key: "overview", href: "/", icon: "grid" },
  { key: "machines", href: "/machines", icon: "server" },
  { key: "services", href: "/services", icon: "box" },
  { key: "domains", href: "/domaines", icon: "globe" },
  { key: "backups", href: "/sauvegardes", icon: "archive" },
  { key: "jobs", href: "/taches", icon: "clock" },
  { key: "alerts", href: "/alertes", icon: "bell" },
];

const settingsEntries: NavEntry[] = [{ key: "settings", href: "/parametres", icon: "settings" }];

// Barre latérale : marque, sections Vue et Réglages avec leurs compteurs en
// mono (rouge quand il y a une alerte), en bas thème, langue et compte.
function Sidebar() {
  const { t, language, languages, setLanguage } = useI18n();
  const counts = useResource<Counts>("/api/counts").data;
  return (
    <aside className="side">
      <NavLink className="brand" to="/">
        <span className="mark">
          <Mark />
        </span>
        <span className="wordmark">
          <span>open</span>
          <b>Cloud</b>
        </span>
      </NavLink>
      <div className="side-label">{t("nav.section_view")}</div>
      <nav className="nav">
        {viewEntries.map((entry) => (
          <NavItem key={entry.key} entry={entry} counter={counter(entry.key, counts)} />
        ))}
      </nav>
      <div className="side-label">{t("nav.section_settings")}</div>
      <nav className="nav">
        {settingsEntries.map((entry) => (
          <NavItem key={entry.key} entry={entry} counter={null} />
        ))}
      </nav>
      <div className="side-foot">
        <div className="switches">
          <div className="theme" role="group">
            <button type="button" data-set-theme="light" aria-label={t("theme.light")} title={t("theme.light")}>
              <Icon name="sun" />
            </button>
            <button type="button" data-set-theme="dark" aria-label={t("theme.dark")} title={t("theme.dark")}>
              <Icon name="moon" />
            </button>
          </div>
          <div className="theme lang" role="group" aria-label={t("language.label")}>
            {languages.map((code) => (
              <button
                key={code}
                type="button"
                className={code === language ? "active" : undefined}
                aria-current={code === language ? "true" : undefined}
                title={t(`language.${code}`)}
                aria-label={t(`language.${code}`)}
                onClick={() => void setLanguage(code)}
              >
                {code}
              </button>
            ))}
          </div>
        </div>
        <div className="account">
          <span className="avatar" aria-hidden="true">
            A
          </span>
          <span className="who">
            <b>{t("account.name")}</b>
            <span>{t("account.role")}</span>
          </span>
        </div>
      </div>
    </aside>
  );
}

interface Counter {
  value: number;
  hot: boolean;
}

// Les machines comptent toutes, en rouge dès qu'une est hors ligne ; les
// tâches ne comptent que celles à traiter. Rien tant que l'API n'a pas
// répondu, plutôt qu'un zéro inventé.
function counter(key: string, counts: Counts | null): Counter | null {
  if (!counts) {
    return null;
  }
  if (key === "machines" && counts.machines.total > 0) {
    return { value: counts.machines.total, hot: counts.machines.online < counts.machines.total };
  }
  if (key === "jobs" && counts.jobs.attention > 0) {
    return { value: counts.jobs.attention, hot: true };
  }
  return null;
}

function NavItem({ entry, counter }: { entry: NavEntry; counter: Counter | null }) {
  const { t } = useI18n();
  return (
    <NavLink className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")} to={entry.href} end={entry.href === "/"}>
      <Icon name={entry.icon} />
      <span>{t(`nav.${entry.key}`)}</span>
      {counter && <span className={counter.hot ? "count hot" : "count"}>{counter.value}</span>}
    </NavLink>
  );
}
