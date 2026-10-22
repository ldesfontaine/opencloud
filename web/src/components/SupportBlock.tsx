import type { Lifecycle } from "../api/lifecycle";
import { useNow } from "../hooks/useNow";
import { useI18n } from "../i18n/context";
import { lifecycleText, lifecycleTones, newerLine, type Subject } from "../lib/lifecycle";
import { ago } from "../lib/time";
import { Pill } from "./Pill";
import "./SupportBlock.scss";

// Le support de la version qui tourne, pour l'image d'un conteneur ou le
// système d'une machine : la branche, jusqu'à quand elle est maintenue,
// les branches maintenues au-dessus. À titre d'information : aucune
// commande. La source est nommée et liée : c'est elle qui le dit, pas
// openCloud.
export function SupportBlock({ lifecycle, subject }: { lifecycle: Lifecycle; subject: Subject }) {
  const { t } = useI18n();
  return (
    <div className="support">
      <span className="support-title">{subject === "system" ? t("lifecycle.title_system") : t("lifecycle.title")}</span>
      <SupportState lifecycle={lifecycle} subject={subject} />
      <SupportDetails lifecycle={lifecycle} />
    </div>
  );
}

// L'état du support et sa phrase, sur une ligne : ce que l'en-tête d'une
// machine garde du bloc. Trop longue, la phrase se coupe d'une ellipse, son
// texte entier en infobulle (et sous l'historique, dans SupportDetails).
export function SupportLine({ lifecycle, subject }: { lifecycle: Lifecycle; subject: Subject }) {
  const { t, language } = useI18n();
  const text = lifecycleText(t, lifecycle, language, subject);
  return (
    <div className="support-line">
      <Pill tone={lifecycleTones[lifecycle.status]}>{t(`lifecycle.status_${lifecycle.status}`)}</Pill>
      <span title={text}>{text}</span>
    </div>
  );
}

function SupportState({ lifecycle, subject }: { lifecycle: Lifecycle; subject: Subject }) {
  const { t, language } = useI18n();
  return (
    <div className="support-state">
      <Pill tone={lifecycleTones[lifecycle.status]}>{t(`lifecycle.status_${lifecycle.status}`)}</Pill>
      <span>{lifecycleText(t, lifecycle, language, subject)}</span>
    </div>
  );
}

// Le reste du support : la dernière version de la branche, les branches
// maintenues au-dessus, la lecture trop vieille, et d'où vient la date.
export function SupportDetails({ lifecycle }: { lifecycle: Lifecycle }) {
  const { t, language } = useI18n();
  const now = useNow();
  const isKnown = lifecycle.status === "maintained" || lifecycle.status === "eol";
  return (
    <>
      {isKnown && lifecycle.latest !== "" && <span>{t("lifecycle.latest", lifecycle.cycle, lifecycle.latest)}</span>}
      {lifecycle.newer.length > 0 && (
        <div className="support-newer">
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
    </>
  );
}
