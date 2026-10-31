import { KeyValue } from "../../components/Card";
import { useI18n } from "../../i18n/context";
import { SettingsSection } from "./SettingsSection";

// À propos : la version du serveur et son commit. L'interface est embarquée
// dans le binaire, elle n'a pas de version à elle. Rien à régler, et la
// session suffit : la section est toujours là.
export function AboutSection() {
  const { t, version, commit } = useI18n();
  return (
    <SettingsSection id="a-propos" titleKey="settings.section_about" text={t("settings.about_intro")}>
      <KeyValue label={t("settings.about_version")} value={version} mono />
      <KeyValue label={t("settings.about_commit")} value={commit === "" ? t("settings.about_commit_unknown") : commit} mono={commit !== ""} />
    </SettingsSection>
  );
}
