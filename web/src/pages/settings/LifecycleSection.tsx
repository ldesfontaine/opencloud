import { saveLifecycleSettings, type LifecycleSettings } from "../../api/settings";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { SettingBlock, SettingFoot, SettingRow } from "../../components/SettingRow";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { readingText } from "../../lib/lifecycle";
import { SettingsSection } from "./SettingsSection";

// La fin de support : activée tant que l'opérateur ne la coupe pas. La
// section dit ce qui sort de la machine openCloud, permet de couper, et le
// pied dit ce que la dernière lecture a donné.
export function LifecycleSection({ settings, reload }: { settings: LifecycleSettings; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const action = useAction();
  // On relit même après un refus : le réglage a pu changer ailleurs.
  const setEnabled = (enabled: boolean) => {
    action.run(() => saveLifecycleSettings(enabled), { onSettled: reload });
  };
  return (
    <SettingsSection
      id="fin-de-support"
      titleKey="settings.section_lifecycle"
      text={t("lifecycle.settings_intro")}
      aside={<Pill tone={settings.enabled ? "ok" : "neutral"}>{t(settings.enabled ? "lifecycle.state_enabled" : "lifecycle.state_disabled")}</Pill>}
    >
      <SettingRow label={t("lifecycle.source_label", settings.source)} description={t("lifecycle.settings_cost", settings.source)}>
        {settings.enabled ? (
          <Button icon="x" onClick={() => setEnabled(false)} disabled={action.busy}>
            {t("lifecycle.disable")}
          </Button>
        ) : (
          <Button variant="primary" icon="check" onClick={() => setEnabled(true)} disabled={action.busy}>
            {t("lifecycle.enable")}
          </Button>
        )}
      </SettingRow>
      {action.error !== null && (
        <SettingBlock>
          <Failure error={action.error} />
        </SettingBlock>
      )}
      {settings.enabled && <SettingFoot note={readingText(t, settings, now)} />}
    </SettingsSection>
  );
}
