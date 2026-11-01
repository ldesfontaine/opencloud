import type { DiskSettings } from "../../api/settings";
import { useT } from "../../i18n/context";
import { ChannelsBlock } from "./ChannelsBlock";
import { DiskThresholdsBlock } from "./DiskThresholdsBlock";
import { MutesBlock } from "./MutesBlock";
import { SettingsSection } from "./SettingsSection";

// Paramètres › Notifications : tout ce qui règle comment on est prévenu.
// Les canaux où partent les alertes, les silences qui les taisent, et les
// seuils du disque qui les ouvrent. Chaque bloc lit sa ressource : l'un qui
// échoue ne retient pas les autres. Les seuils manquent si leur lecture a
// échoué, comme avant.
export function NotificationsSection({ disks, reloadDisks }: { disks: DiskSettings | null; reloadDisks: () => void }) {
  const t = useT();
  return (
    <SettingsSection id="notifications" titleKey="settings.section_notifications" text={t("settings.notifications_intro")}>
      <ChannelsBlock />
      <MutesBlock />
      {disks !== null && <DiskThresholdsBlock settings={disks} reload={reloadDisks} />}
    </SettingsSection>
  );
}
