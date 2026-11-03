import { Link } from "react-router";

import type { UndeclaredGroup } from "../../api/directory";
import type { Machine } from "../../api/machines";
import { Button } from "../../components/Button";
import { Field } from "../../components/Field";
import { Select } from "../../components/Select";
import { useT } from "../../i18n/context";
import { pickableContainers } from "../../lib/gaps";
import { machinesWithUnits } from "../../lib/systemd";

// La première chose que dit « Déclarer un service » : ce que la machine fait
// déjà tourner. Choisir un conteneur remplit tout le reste (image, ports,
// projet Compose) ; un binaire sous systemd se choisit dans les unités de sa
// machine (un lien par machine qui en a relevé) ; « saisir à la main » ouvre
// l'assistant vide, pour un service que la machine ne montre pas encore.
export function ContainerChoice({
  groups,
  machines,
  onPick,
  onManual,
}: {
  groups: UndeclaredGroup[];
  machines: readonly Pick<Machine, "id" | "name" | "modules">[];
  onPick: (containerID: string) => void;
  onManual: () => void;
}) {
  const t = useT();
  const withUnits = machinesWithUnits(machines);
  return (
    <div className="container-choice">
      <Field label={t("directory.pick_label")} htmlFor="service-pick" help={t("directory.pick_help")}>
        <Select id="service-pick" icon="box" value="" placeholder={t("directory.pick_placeholder")} options={pickableContainers(groups)} onChange={onPick} />
      </Field>
      {withUnits.length > 0 && (
        <div className="cluster">
          <span className="secondary">{t("directory.pick_units")}</span>
          {withUnits.map((machine) => (
            <Link key={machine.id} to={`/machines/${machine.id}/systemd`}>
              {machine.name}
            </Link>
          ))}
        </div>
      )}
      <div className="cluster">
        <Button variant="ghost" icon="plus" onClick={onManual}>
          {t("directory.pick_manual")}
        </Button>
        <span className="secondary">{t("directory.pick_manual_help")}</span>
      </div>
    </div>
  );
}
