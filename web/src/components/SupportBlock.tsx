import type { Lifecycle } from "../api/types";
import { useNow } from "../hooks/useNow";
import { useI18n } from "../i18n/context";
import { lifecycleText, lifecycleTones, newerLine } from "../lib/lifecycle";
import { ago } from "../lib/time";
import { Pill } from "./Pill";

// Le support de la version qui tourne : la branche, jusqu'à quand elle est
// maintenue, vers quelles branches monter. La source est nommée et liée :
// c'est elle qui le dit, pas openCloud.
export function SupportBlock({ lifecycle }: { lifecycle: Lifecycle }) {
  const { t, language } = useI18n();
  const now = useNow();
  const isKnown = lifecycle.status === "maintained" || lifecycle.status === "eol";
  return (
    <div className="update-support">
      <span className="update-support-title">{t("lifecycle.title")}</span>
      <div className="update-state">
        <Pill tone={lifecycleTones[lifecycle.status]}>{t(`lifecycle.status_${lifecycle.status}`)}</Pill>
        <span>{lifecycleText(t, lifecycle, language)}</span>
      </div>
      {isKnown && lifecycle.latest !== "" && <span>{t("lifecycle.latest", lifecycle.cycle, lifecycle.latest)}</span>}
      {lifecycle.newer.length > 0 && (
        <div className="update-newer">
          <span>{t("lifecycle.newer_title")}</span>
          {lifecycle.newer.map((newer) => (
            <span key={newer.cycle}>{newerLine(t, newer, language)}</span>
          ))}
        </div>
      )}
      {lifecycle.is_stale && <span className="secondary">{t("lifecycle.stale")}</span>}
      {lifecycle.fetched_at !== null && (
        <span className="secondary">
          {t("lifecycle.read_ago", ago(t, lifecycle.fetched_at, now))}
          {lifecycle.product_url !== "" && (
            <>
              {" · "}
              <a href={lifecycle.product_url} target="_blank" rel="noopener noreferrer">
                {t("lifecycle.source_link")}
              </a>
            </>
          )}
        </span>
      )}
    </div>
  );
}
