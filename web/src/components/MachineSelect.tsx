import type { Machine } from "../api/machines";
import { useT } from "../i18n/context";
import { machineOption } from "../lib/machines";
import type { Unfit } from "../lib/rights";
import { Select, type SelectProps } from "./Select";

// Le choix d'une machine, par son nom. noneLabel ajoute en tête l'option
// vide et dit ce qu'elle veut dire : « aucune » dans un formulaire, « toutes »
// dans un filtre ; sans lui, il faut choisir une machine.
// fit : ce que le choix exige de la machine (unfitFor). Une machine qui ne
// convient pas reste dans la liste, grisée, avec la raison et le lien qui y
// remédie : l'opérateur voit pourquoi elle manque, au lieu de la chercher.
export function MachineSelect<M extends Pick<Machine, "id" | "name" | "kind">>({
  machines,
  noneLabel,
  fit,
  ...rest
}: Omit<SelectProps, "options" | "placeholder"> & {
  machines: readonly M[];
  noneLabel?: string;
  fit?: (machine: M) => Unfit | null;
}) {
  const t = useT();
  const options = machines.map((machine) => {
    const unfit = fit?.(machine) ?? null;
    const option = machineOption(t, machine);
    return unfit === null ? option : { ...option, disabled: true, reason: unfit.reason, link: unfit.link };
  });
  if (noneLabel !== undefined) {
    options.unshift({ value: "", label: noneLabel });
  }
  return <Select options={options} {...rest} />;
}
