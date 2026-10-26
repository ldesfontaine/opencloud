import { useEffect, useState, type ReactNode } from "react";

import { useDeclaredServices } from "../api/directory";
import { useJobs } from "../api/jobs";
import { useMachines } from "../api/machines";
import { useProbes } from "../api/probes";
import type { MemberKind } from "../api/status";
import { useT } from "../i18n/context";
import { nameWithMachine } from "../lib/objects";
import { memberKinds, type MemberChoice } from "../lib/status";
import { Button } from "./Button";
import { EnumSelect, Select } from "./Select";
import "./ObjectPicker.scss";

interface Candidate {
  id: string;
  name: string;
}

interface ObjectPickerProps {
  value: MemberChoice[];
  onChange: (value: MemberChoice[]) => void;
  // Le début des clés qui nomment chaque genre : « component.kind_ ».
  labelKeyPrefix: string;
  // Le nom du champ pour un lecteur d'écran, et le bouton qui ajoute.
  label: string;
  addLabel: string;
  // Dit quoi faire de la liste vide ; rien, si elle peut rester vide.
  emptyText?: string;
  // Le nom d'un objet que les listes du serveur ne connaissent pas (ou plus).
  nameOf: (choice: MemberChoice) => string;
  // Ce que la ligne d'un objet montre après son nom : une pastille.
  detailOf?: (choice: MemberChoice) => ReactNode;
}

// Choisir des objets de l'annuaire (machine, service déclaré, tâche, sonde)
// parmi ce qu'openCloud connaît déjà. Un composant y rattache ses objets, un
// incident y vise les siens.
export function ObjectPicker({ value, onChange, labelKeyPrefix, label, addLabel, emptyText, nameOf, detailOf }: ObjectPickerProps) {
  const t = useT();
  const machines = useMachines();
  // Un « service » est un service déclaré : il survit à la recréation de ses conteneurs.
  const services = useDeclaredServices();
  const jobs = useJobs();
  const probes = useProbes();
  const [pickKind, setPickKind] = useState<MemberKind>("probe");
  const [pickID, setPickID] = useState("");

  const candidates: Record<MemberKind, Candidate[]> = {
    machine: machines.data?.machines ?? [],
    service: (services.data?.services ?? []).map((service) => ({ id: service.id, name: nameWithMachine({ name: service.title !== "" ? service.title : service.name, machine_name: service.machine_name }) })),
    heartbeat: (jobs.data?.jobs ?? []).map((job) => ({ id: job.id, name: nameWithMachine(job) })),
    probe: (probes.data?.probes ?? []).map((probe) => ({ id: probe.id, name: nameWithMachine(probe) })),
  };
  const nameFor = (choice: MemberChoice) => candidates[choice.kind].find((candidate) => candidate.id === choice.id)?.name ?? nameOf(choice);
  const available = candidates[pickKind].filter((candidate) => !value.some((choice) => choice.kind === pickKind && choice.id === candidate.id));

  useEffect(() => {
    setPickID("");
  }, [pickKind]);

  const add = () => {
    if (pickID === "") {
      return;
    }
    onChange([...value, { kind: pickKind, id: pickID }]);
    setPickID("");
  };

  return (
    <>
      {value.length === 0 ? (
        emptyText !== undefined && <span className="secondary">{emptyText}</span>
      ) : (
        <div className="members">
          {value.map((choice) => (
            <div className="member" key={`${choice.kind}:${choice.id}`}>
              <span className="kind">{t(`${labelKeyPrefix}${choice.kind}`)}</span>
              <span className="medium">{nameFor(choice)}</span>
              {detailOf?.(choice) ?? <span />}
              <Button small variant="ghost" icon="x" onClick={() => onChange(value.filter((candidate) => candidate !== choice))} aria-label={t("component.remove_object")} />
            </div>
          ))}
        </div>
      )}
      <div className="member-pick">
        <EnumSelect value={pickKind} values={memberKinds} labelKey={labelKeyPrefix} onChange={setPickKind} aria-label={label} />
        <Select
          value={pickID}
          options={[{ value: "", label: t("component.pick_object") }, ...available.map((candidate) => ({ value: candidate.id, label: candidate.name }))]}
          onChange={setPickID}
          aria-label={t("component.pick_object")}
        />
        <Button icon="plus" onClick={add} disabled={pickID === ""}>
          {addLabel}
        </Button>
      </div>
    </>
  );
}
