import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router";

import { useAuth, type AuthState } from "../api/auth";
import { useCounts } from "../api/counts";
import { useLive } from "../hooks/useLive";
import { useTheme } from "../hooks/useTheme";
import { useI18n, useT } from "../i18n/context";
import { accessLabel } from "../lib/auth";
import { nextDrawer, returnsFocus, type DrawerEvent } from "../lib/drawer";
import { menuEntries, settingsEntry, type MenuEntry } from "../lib/modules";
import { Icon, Mark } from "./Icon";
import { Wordmark } from "./Wordmark";
import "./Layout.scss";

// L'état de l'accès est lu une fois ici : la barre latérale le dit.
export function Layout() {
  useLive();
  const access = useAuth().data;
  const drawer = useDrawer();
  return (
    <div className={drawer.open ? "app drawer-open" : "app"}>
      <TopBar drawer={drawer} />
      {drawer.narrow && drawer.open && <div className="scrim" aria-hidden="true" onClick={() => drawer.close("backdrop")} />}
      <Sidebar access={access} inert={drawer.narrow && !drawer.open} />
      <main className="main" id="main" inert={drawer.narrow && drawer.open}>
        <div className="content">
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

// La marque, aussi en tête de l'écran de première configuration.
export function Brand() {
  return (
    <NavLink className="brand" to="/">
      <span className="mark">
        <Mark />
      </span>
      <Wordmark />
    </NavLink>
  );
}

// Barre latérale : marque, puis le menu que le registre des modules compose
// (les entrées fixes et celles des modules configurés) avec leurs compteurs en
// mono (rouge quand il y a une alerte) ; en bas Paramètres, thème, langue et compte.
function Sidebar({ access, inert }: { access: AuthState | null; inert: boolean }) {
  const { t, language, languages, setLanguage } = useI18n();
  const theme = useTheme();
  const label = accessLabel(access);
  const counts = useCounts().data;
  return (
    <aside className="side" id="side" inert={inert}>
      <Brand />
      <nav className="nav">
        {menuEntries(counts).map((entry) => (
          <NavItem key={entry.key} entry={entry} />
        ))}
      </nav>
      <div className="side-foot">
        <nav className="nav">
          <NavItem entry={settingsEntry} />
        </nav>
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
          <NavLink className="account" to="/parametres">
            <span className="avatar" aria-hidden="true">
              <Icon name="shield" />
            </span>
            <span className="who">
              <b>{t(label.key)}</b>
              <span>{label.username}</span>
            </span>
          </NavLink>
        )}
      </div>
    </aside>
  );
}

function NavItem({ entry }: { entry: MenuEntry }) {
  const { t } = useI18n();
  const counter = entry.counter;
  return (
    <NavLink className={({ isActive }) => (isActive ? "nav-item active" : "nav-item")} to={entry.href} end={entry.href === "/"}>
      <Icon name={entry.icon} />
      <span>{t(entry.label)}</span>
      {counter && <span className={counter.hot ? "count hot" : "count"}>{counter.value}</span>}
      {entry.toConfigure && !counter && <span className="to-configure" role="img" aria-label={t("nav.to_configure")} title={t("nav.to_configure")} />}
    </NavLink>
  );
}
