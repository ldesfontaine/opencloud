import { useEffect, useState } from "react";

import { saveReminderSettings, type ReminderSettings } from "../../api/thresholds";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { Input } from "../../components/Field";
import { Pill } from "../../components/Pill";
import { SettingBlock, SettingFoot, SettingRow } from "../../components/SettingRow";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";

// Le rappel d'une alerte de danger ouverte, non lue et non tue : renvoyée une
// seule fois aux canaux, « toujours en cours », passé ce délai. Zéro coupe le
// rappel.
export function ReminderBlock({ settings, reload }: { settings: ReminderSettings; reload: () => void }) {
  const t = useT();
  const [hours, setHours] = useState(String(settings.hours));
  const [refusal, setRefusal] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const action = useAction();
  useEffect(() => {
    setHours(String(settings.hours));
  }, [settings.hours]);
  const changed = hours !== String(settings.hours);

  const submit = () => {
    setSaved(false);
    const value = Number(hours);
    const isValid = Number.isInteger(value) && value >= 0 && value <= settings.max_hours;
    setRefusal(isValid ? null : "settings.reminder_invalid");
    if (!isValid) {
      return;
    }
    action.run(() => saveReminderSettings(value), {
      onDone: () => setSaved(true),
      onSettled: reload,
    });
  };
  const error = refusal ?? action.error;
  return (
    <>
      <div className="setting-subhead">
        <span className="card-t">{t("settings.reminder_title")}</span>
        <span className="secondary">{t("settings.reminder_intro")}</span>
      </div>
      <form
        className="setting-stack"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <SettingRow label={t("settings.reminder_hours_label")} description={t("settings.reminder_hours_help", settings.max_hours)} htmlFor="reminder-hours" width="short">
          <Input id="reminder-hours" type="number" inputMode="numeric" min={0} max={settings.max_hours} value={hours} onChange={(event) => setHours(event.target.value)} required />
        </SettingRow>
        <SettingBlock>
          <p className="secondary">{settings.hours === 0 ? t("settings.reminder_off") : t("settings.reminder_on", settings.hours)}</p>
        </SettingBlock>
        {error !== null && (
          <SettingBlock>
            <Failure error={error} />
          </SettingBlock>
        )}
        <SettingFoot note={saved && !changed ? <Pill tone="ok">{t("thresholds.saved")}</Pill> : undefined}>
          <Button type="submit" variant="primary" icon="check" disabled={action.busy || !changed}>
            {t("thresholds.save")}
          </Button>
        </SettingFoot>
      </form>
    </>
  );
}
