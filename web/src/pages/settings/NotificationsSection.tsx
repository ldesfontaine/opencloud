import type { ReminderSettings, ThresholdRules } from "../../api/thresholds";
import { useT } from "../../i18n/context";
import { ChannelsBlock } from "./ChannelsBlock";
import { MutesBlock } from "./MutesBlock";
import { ReminderBlock } from "./ReminderBlock";
import { SettingsSection } from "./SettingsSection";
import { ThresholdsBlock } from "./ThresholdsBlock";

// Paramètres › Notifications : tout ce qui règle comment on est prévenu.
// Les canaux où partent les alertes, les silences qui les taisent, les
// seuils qui les ouvrent et le rappel des alertes de danger restées sans
// lecture. Chaque bloc lit sa ressource : l'un qui échoue ne retient pas les
// autres. Les seuils et le rappel manquent si leur lecture a échoué.
export function NotificationsSection({
  thresholds,
  reloadThresholds,
  reminder,
  reloadReminder,
}: {
  thresholds: ThresholdRules | null;
  reloadThresholds: () => void;
  reminder: ReminderSettings | null;
  reloadReminder: () => void;
}) {
  const t = useT();
  return (
    <SettingsSection id="notifications" titleKey="settings.section_notifications" text={t("settings.notifications_intro")}>
      <ChannelsBlock />
      <MutesBlock />
      {thresholds !== null && <ThresholdsBlock rules={thresholds} reload={reloadThresholds} />}
      {reminder !== null && <ReminderBlock settings={reminder} reload={reloadReminder} />}
    </SettingsSection>
  );
}
