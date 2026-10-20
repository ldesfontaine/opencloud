import { saveLifecycleSettings, type LifecycleSettings } from "../../api/settings";
import { Button } from "../../components/Button";
import { Card, CardBody, CardHeader } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { readingText } from "../../lib/lifecycle";

// La fin de support : activée tant que l'opérateur ne la coupe pas. La
// carte dit ce qui sort de la machine openCloud, ce que la dernière
// lecture a donné, et permet de couper.
export function LifecycleSection({ settings, reload }: { settings: LifecycleSettings; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const action = useAction();
  // On relit même après un refus : le réglage a pu changer ailleurs.
  const setEnabled = (enabled: boolean) => {
    action.run(() => saveLifecycleSettings(enabled), { onSettled: reload });
  };
  return (
    <Card className="form-card">
      <CardHeader
        title={t("lifecycle.settings_title")}
        aside={<Pill tone={settings.enabled ? "ok" : "neutral"}>{t(settings.enabled ? "lifecycle.state_enabled" : "lifecycle.state_disabled")}</Pill>}
      />
      <CardBody>
        <p>{t("lifecycle.settings_intro")}</p>
        <p className="secondary">{t("lifecycle.settings_cost", settings.source)}</p>
        <Failure error={action.error} />
        {settings.enabled ? (
          <div className="cluster">
            <span className="secondary">{readingText(t, settings, now)}</span>
            <Button icon="x" onClick={() => setEnabled(false)} disabled={action.busy}>
              {t("lifecycle.disable")}
            </Button>
          </div>
        ) : (
          <div className="cluster">
            <Button variant="primary" icon="check" onClick={() => setEnabled(true)} disabled={action.busy}>
              {t("lifecycle.enable")}
            </Button>
          </div>
        )}
      </CardBody>
    </Card>
  );
}
