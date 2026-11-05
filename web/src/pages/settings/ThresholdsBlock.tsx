import { saveThreshold, type ThresholdRules } from "../../api/thresholds";
import { ThresholdForm } from "../../components/ThresholdForm";
import { useI18n } from "../../i18n/context";
import { resourceOrder } from "../../lib/thresholds";

// Les seuils d'alerte des ressources, un bloc de Paramètres › Notifications :
// un formulaire par ressource, chacun enregistré à part. Ce sont les seuils de
// toutes les machines qui n'ont pas les leurs ; la fiche d'une machine
// porte sa surcharge.
export function ThresholdsBlock({ rules, reload }: { rules: ThresholdRules; reload: () => void }) {
  const { t, reloadSession } = useI18n();
  return (
    <>
      <div className="setting-subhead">
        <span className="card-t">{t("settings.thresholds_title")}</span>
        <span className="secondary">{t("settings.thresholds_intro")}</span>
      </div>
      {resourceOrder.map((resource) => {
        const rule = rules.rules.find((candidate) => candidate.resource === resource);
        if (rule === undefined) {
          return null;
        }
        return (
          <section key={resource} className="threshold-section" aria-labelledby={`threshold-${resource}`}>
            <h4 id={`threshold-${resource}`} className="threshold-name">
              {t(`thresholds.resource_${resource}`)}
            </h4>
            <ThresholdForm
              rule={rule}
              idPrefix={`threshold-${resource}`}
              save={(request) => saveThreshold(resource, request)}
              // Les jauges lisent les seuils dans la session : elle se relit
              // avec eux, après la relecture des seuils.
              onSaved={() => {
                reload();
                reloadSession();
              }}
            />
          </section>
        );
      })}
    </>
  );
}
