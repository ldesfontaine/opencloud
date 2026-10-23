import { useEffect, useRef } from "react";

import { useT } from "../../i18n/context";
import { settingsSections, type SettingsSectionId } from "../../lib/settingsSections";

// La navigation de Paramètres : un lien par section présente, vers son
// ancre. Large, une colonne collante à gauche ; étroit, une rangée d'onglets
// qui défile. Le lien de la section lue est marqué (aria-current).
export function SettingsNav({ present, current, onPick }: { present: readonly SettingsSectionId[]; current: string | null; onPick: (id: string) => void }) {
  const t = useT();
  const bar = useRef<HTMLElement>(null);

  // Sur une rangée qui défile, le lien marqué reste visible : on ne bouge
  // que la rangée, jamais la page.
  useEffect(() => {
    const link = bar.current?.querySelector<HTMLElement>("[aria-current='location']");
    const row = bar.current;
    if (link === null || link === undefined || row === null) {
      return;
    }
    if (link.offsetLeft < row.scrollLeft || link.offsetLeft + link.offsetWidth > row.scrollLeft + row.clientWidth) {
      row.scrollTo({ left: Math.max(0, link.offsetLeft - 16) });
    }
  }, [current]);

  return (
    <nav className="settings-nav" aria-label={t("settings.nav_label")} ref={bar}>
      {settingsSections
        .filter((section) => present.includes(section.id))
        .map((section) => (
          <a key={section.id} href={`#${section.id}`} className="settings-nav-link" onClick={() => onPick(section.id)} {...(current === section.id && { "aria-current": "location" as const })}>
            {t(section.titleKey)}
          </a>
        ))}
    </nav>
  );
}
