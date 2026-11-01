import type { UndeclaredGroup } from "../../api/directory";
import { Button } from "../../components/Button";
import { Field } from "../../components/Field";
import { Select } from "../../components/Select";
import { useT } from "../../i18n/context";
import { pickableContainers } from "../../lib/gaps";

// La première chose que dit « Déclarer un service » : ce que la machine fait
// déjà tourner. Choisir un conteneur remplit tout le reste (image, ports,
// projet Compose) ; « saisir à la main » ouvre l'assistant vide, pour un
// binaire sous systemd ou un service que la machine ne montre pas encore.
export function ContainerChoice({ groups, onPick, onManual }: { groups: UndeclaredGroup[]; onPick: (containerID: string) => void; onManual: () => void }) {
  const t = useT();
  return (
    <div className="container-choice">
      <Field label={t("directory.pick_label")} htmlFor="service-pick" help={t("directory.pick_help")}>
        <Select id="service-pick" icon="box" value="" placeholder={t("directory.pick_placeholder")} options={pickableContainers(groups)} onChange={onPick} />
      </Field>
      <div className="cluster">
        <Button variant="ghost" icon="plus" onClick={onManual}>
          {t("directory.pick_manual")}
        </Button>
        <span className="secondary">{t("directory.pick_manual_help")}</span>
      </div>
    </div>
  );
}
