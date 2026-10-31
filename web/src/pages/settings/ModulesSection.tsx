import type { ModuleState } from "../../api/counts";
import { Button } from "../../components/Button";
import { Pill } from "../../components/Pill";
import { SettingRow } from "../../components/SettingRow";
import { useT } from "../../i18n/context";
import { moduleHref, moduleSetupHref } from "../../lib/modules";
import { SettingsSection } from "./SettingsSection";

// Paramètres › Modules : les modules de l'installation et s'ils sont
// configurés. Un module n'entre au menu qu'une fois configuré ; ici, on le
// configure : le bouton mène à son premier geste, ou à sa page quand il l'est
// déjà. Les modules d'une machine viennent de son rôle, pas d'ici.
export function ModulesSection({ modules }: { modules: ModuleState[] }) {
  const t = useT();
  return (
    <SettingsSection id="modules" titleKey="settings.section_modules" text={t("settings.modules_intro")}>
      {modules.map((state) => {
        const href = state.configured ? moduleHref(state.id) : moduleSetupHref(state.id);
        return (
          <SettingRow key={state.id} label={t(`module.${state.id}`)} description={t(`module.${state.id}_text`)}>
            <Pill tone={state.configured ? "ok" : "neutral"}>{t(state.configured ? "settings.modules_configured" : "settings.modules_todo")}</Pill>
            {href !== null && (
              <Button small to={href}>
                {t(state.configured ? "settings.modules_open" : "settings.modules_configure")}
              </Button>
            )}
          </SettingRow>
        );
      })}
    </SettingsSection>
  );
}
