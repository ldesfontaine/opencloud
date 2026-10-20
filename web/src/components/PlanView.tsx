import type { ManifestChange } from "../api/manifest";
import { useI18n } from "../i18n/context";
import { carriesText, changeLabel, fieldText } from "../lib/manifest";
import type { Tone } from "../lib/tone";
import { Pill } from "./Pill";
import { List } from "./Table";
import "./PlanView.scss";

const actionTones: Record<ManifestChange["action"], Tone> = {
  create: "ok",
  update: "accent",
  delete: "danger",
};

// Les changements d'un plan dans l'ordre où ils s'écrivent : le geste, l'objet,
// les champs avant → après, les conséquences, et ce qu'une suppression
// emporte. Avec `done`, un passage arrêté marque ce qui est écrit.
export function PlanView({ changes, done }: { changes: ManifestChange[]; done?: number }) {
  const { t, language } = useI18n();
  return (
    <List>
      {changes.map((change, index) => (
        <div className="plan-change" key={`${change.kind}-${change.name}-${index}`}>
          <div className="plan-head">
            <Pill tone={actionTones[change.action]} dot={false}>
              {t(`manifest.action_${change.action}`)}
            </Pill>
            <b>{changeLabel(t, change)}</b>
            {change.id !== "" && <span className="mono muted">{change.id}</span>}
            {done !== undefined && <span className="muted">{t(index < done ? "manifest.change_written" : "manifest.change_not_written")}</span>}
          </div>
          {change.action === "update" && change.fields.length > 0 && (
            <ul className="plan-fields mono">
              {change.fields.map((field) => (
                <li key={field.field}>{fieldText(field)}</li>
              ))}
            </ul>
          )}
          {change.action === "delete" && change.carries.length > 0 && (
            <p className="plan-note">
              {t("manifest.takes_along")} {carriesText(t, language, change.carries)}
            </p>
          )}
          {change.notes.map((note) => (
            <p className="plan-note" key={note}>
              {t(`manifest.note_${note}`)}
            </p>
          ))}
        </div>
      ))}
    </List>
  );
}
