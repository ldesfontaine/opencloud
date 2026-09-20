import type { ProbeDay } from "../api/types";
import { useT } from "../i18n/context";
import { dayKeys, dayTone } from "../lib/probes";
import "./UptimeBar.scss";

// La barre de disponibilité : un segment par jour UTC, du plus ancien à
// aujourd'hui. Un jour sans essai reste gris, jamais rouge : on ne sait
// pas, on ne l'invente pas.
export function UptimeBar({ days, span, now }: { days: ProbeDay[]; span: number; now: number }) {
  const t = useT();
  const byDay = new Map(days.map((day) => [day.day, day]));
  return (
    <div className="ubar" role="img" aria-label={t("probe.uptime_title")}>
      {dayKeys(span, now).map((key) => {
        const day = byDay.get(key);
        const label = day === undefined || day.total === 0 ? t("probe.bar_nodata", key) : t("probe.bar_day", key, day.total);
        return <span className={`ubar-seg ubar-${dayTone(day)}`} key={key} title={label} />;
      })}
    </div>
  );
}
