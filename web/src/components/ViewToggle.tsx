import { useId } from "react";

import { useT } from "../i18n/context";
import type { ViewMode } from "../lib/viewMode";
import { Icon, type IconName } from "./Icon";
import "./Field.scss";
import "./ViewToggle.scss";

const modes: { mode: ViewMode; icon: IconName; label: string }[] = [
  { mode: "cards", icon: "grid", label: "entity.view_cards" },
  { mode: "table", icon: "list", label: "entity.view_table" },
];

// La bascule cartes / tableau d'une liste : le segment des filtres, en
// icônes ; le nom de chaque vue en info-bulle et pour le lecteur d'écran.
// Le choix se garde par useViewMode.
export function ViewToggle({ mode, onChange }: { mode: ViewMode; onChange: (mode: ViewMode) => void }) {
  const t = useT();
  const group = useId();
  return (
    <span className="seg view-toggle" role="radiogroup" aria-label={t("entity.view")}>
      {modes.map((choice) => (
        <label key={choice.mode} data-tip={t(choice.label)}>
          <input type="radio" name={group} checked={mode === choice.mode} aria-label={t(choice.label)} onChange={() => onChange(choice.mode)} />
          <span>
            <Icon name={choice.icon} />
          </span>
        </label>
      ))}
    </span>
  );
}
