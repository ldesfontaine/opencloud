import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";

import type { AuthState, Counts } from "../api/types";
import { useLive } from "../hooks/useLive";
import { useResource } from "../hooks/useResource";
import { useTheme } from "../hooks/useTheme";
import { useI18n, useT } from "../i18n/context";
import { accessLabel, isInterfaceOpen } from "../lib/auth";
import { nextDrawer, returnsFocus, type DrawerEvent } from "../lib/drawer";
import { Icon, Mark, type IconName } from "./Icon";
import "./Layout.scss";

// L'état de l'accès est lu une fois ici : la barre latérale le dit, la
// bannière le rappelle tant que l'interface est ouverte.
export function Layout() {
  useLive();
  const access = useResource<AuthState>("/api/auth").data;
  const drawer = useDrawer();
  return (
    <div className={drawer.open ? "app drawer-open" : "app"}>
      <TopBar drawer={drawer} />
      {drawer.narrow && drawer.open && <div className="scrim" aria-hidden="true" onClick={() => drawer.close("backdrop")} />}
      <Sidebar access={access} inert={drawer.narrow && !drawer.open} />
      <main className="main" id="main" inert={drawer.narrow && drawer.open}>
        <div className="content">
          {isInterfaceOpen(access) && <OpenBanner />}
          <Outlet />
        </div>
      </main>
    </div>
  );
}

// Même seuil que Layout.scss : sous 860 px, la barre latérale devient un
// tiroir.
const narrowQuery = "(max-width: 860px)";

interface Drawer {
  open: boolean;
  narrow: boolean;
  toggle: () => void;
  close: (event: DrawerEvent) => void;
  button: RefObject<HTMLButtonElement | null>;
}

// L'état du tiroir et ce qui le ferme : le voile, Échap, chaque navigation,
// le retour en écran large. Ouvert, le focus entre dans le tiroir ; fermé
// par l'utilisateur, il revient au bouton menu.
function useDrawer(): Drawer {
  const [open, setOpen] = useState(false);
  const [narrow, setNarrow] = useState(() => window.matchMedia(narrowQuery).matches);
  const button = useRef<HTMLButtonElement>(null);
  const openRef = useRef(open);
  useEffect(() => {
    openRef.current = open;
  }, [open]);

  const close = useCallback((event: DrawerEvent) => {
    const wasOpen = openRef.current;
    setOpen((current) => nextDrawer(current, event));
    if (returnsFocus(wasOpen, event)) {
      button.current?.focus();
    }
  }, []);
  const toggle = useCallback(() => setOpen((current) => nextDrawer(current, "toggle")), []);

  const { pathname } = useLocation();
  useEffect(() => close("navigate"), [pathname, close]);

  useEffect(() => {
    const media = window.matchMedia(narrowQuery);
    const onChange = () => {
      setNarrow(media.matches);
      if (!media.matches) {
        close("wide");
      }
    };
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, [close]);

  useEffect(() => {
    if (!open) {
      return;
    }
    document.querySelector<HTMLElement>("#side .nav-item")?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        close("escape");
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, close]);

  return { open, narrow, toggle, close, button };
}

// L'en-tête du téléphone : la marque et le bouton du menu. Caché en écran
// large, où la barre latérale reste en place.
function TopBar({ drawer }: { drawer: Drawer }) {
  const t = useT();
  return (
    <header className="topbar">
      <Brand />
      <button
        ref={drawer.button}
        type="button"
        className="menu-button"
        aria-label={t(drawer.open ? "nav.menu_close" : "nav.menu_open")}
        aria-expanded={drawer.open}
        aria-controls="side"
        onClick={drawer.toggle}
      >
        <Icon name={drawer.open ? "x" : "menu"} />
      </button>
    </header>
  );
}

function Brand() {
  return (
    <NavLink className="brand" to="/">
      <span className="mark">
        <Mark />
      </span>
      <span className="wordmark">
        <span>open</span>
        <b>Cloud</b>
      </span>
    </NavLink>
  );
}

interface NavEntry {
  key: string;
  href: string;
  icon: IconName;
  // Où mène l'entrée quand son compteur demande attention : les services du
  // compteur sont des conteneurs, qui ne sont plus la première page de Services.
  attentionHref?: string;
}

// Seul ce qui est livré a son entrée : les Sauvegardes reviendront avec
// leur fonctionnalité.
const viewEntries: NavEntry[] = [
  { key: "overview", href: "/", icon: "grid" },
  { key: "machines", href: "/machines", icon: "server" },
  { key: "services", href: "/services", icon: "box", attentionHref: "/services/conteneurs" },
  { key: "domains", href: "/domaines", icon: "globe" },
  { key: "jobs", href: "/taches", icon: "clock" },
  { key: "statuspage", href: "/page-statut", icon: "eye" },
  { key: "alerts", href: "/alertes", icon: "bell" },
];

const settingsEntries: NavEntry[] = [{ key: "settings", href: "/parametres", icon: "settings" }];

// Barre latérale : marque, sections Vue et Administration avec leurs compteurs en
// mono (rouge quand il y a une alerte), en bas thème, langue et compte.
function Sidebar({ access, inert }: { access: AuthState | null; inert: boolean }) {
  const { t, language, languages, setLanguage } = useI18n();
  const theme = useTheme();
  const label = accessLabel(access);
  const counts = useResource<Counts>("/api/counts").data;
  return (
    <aside className="side" id="side" inert={inert}>
      <Brand />
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
          <div className="theme" role="group" aria-label={t("theme.label")}>
            <button type="button" data-set-theme="light" aria-pressed={theme === "light"} aria-label={t("theme.light")} title={t("theme.light")}>
              <Icon name="sun" />
            </button>
            <button type="button" data-set-theme="dark" aria-pressed={theme === "dark"} aria-label={t("theme.dark")} title={t("theme.dark")}>
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
        {label !== null && (
          <NavLink className={label.username === null ? "account account-open" : "account"} to="/parametres">
            <span className="avatar" aria-hidden="true">
              <Icon name="shield" />
            </span>
            <span className="who">
              <b>{t(label.key)}</b>
              <span>{label.username ?? t("auth.side_open_hint")}</span>
            </span>
          </NavLink>
        )}
      </div>
    </aside>
  );
}

interface Counter {
  value: number;
  hot: boolean;
}

// Les machines comptent toutes, en rouge dès qu'une est hors ligne ; les
// tâches ne comptent que celles à traiter ; les services et les sondes
// comptent tous, en rouge dès qu'un est défaillant ou hors ligne ; la page
// de statut ne compte que les incidents ouverts ; les alertes comptent les
// ouvertes, en rouge tant qu'une n'est pas acquittée. Rien tant que l'API
// n'a pas répondu, plutôt qu'un zéro inventé.
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
  if (key === "services" && counts.services.total > 0) {
    return { value: counts.services.total, hot: counts.services.attention > 0 };
  }
  if (key === "domains" && counts.probes.total > 0) {
    return { value: counts.probes.total, hot: counts.probes.attention > 0 };
  }
  if (key === "statuspage" && counts.status.open_incidents > 0) {
    return { value: counts.status.open_incidents, hot: true };
  }
  if (key === "alerts" && counts.alerts.open > 0) {
    return { value: counts.alerts.open, hot: counts.alerts.unacknowledged > 0 };
  }
  return null;
}

function NavItem({ entry, counter }: { entry: NavEntry; counter: Counter | null }) {
  const { t } = useI18n();
  const href = counter?.hot === true && entry.attentionHref !== undefined ? entry.attentionHref : entry.href;
  return (
    <NavLink className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")} to={href} end={entry.href === "/"}>
      <Icon name={entry.icon} />
      <span>{t(`nav.${entry.key}`)}</span>
      {counter && <span className={counter.hot ? "count hot" : "count"}>{counter.value}</span>}
    </NavLink>
  );
}

// Rappel tant que personne n'est vérifié : qui atteint le port est
// l'opérateur. Le lien mène à la carte qui y remédie.
function OpenBanner() {
  const t = useT();
  return (
    <div className="note note-warn access-banner" role="status">
      <Icon name="shield" />
      <span>{t("auth.banner")}</span>
      <Link to="/parametres">{t("auth.banner_link")}</Link>
    </div>
  );
}
