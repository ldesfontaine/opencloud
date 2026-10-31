import { useId, useState, type FormEvent } from "react";
import { Navigate, useNavigate, useParams } from "react-router";

import { setMachinePlacement, useMachines, type KindID, type Machine } from "../../api/machines";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { Field } from "../../components/Field";
import { Modal, ModalCancel } from "../../components/Modal";
import { Select } from "../../components/Select";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { hostCandidates, hostOptions, isPlacementModified, kindIDs, placementOf, type PlacementValues } from "../../lib/placement";

// « Corriger le type et l'hôte » : la fenêtre de la fiche d'une machine, à
// l'adresse /machines/:id/emplacement. Le type se déduit (l'agent, l'hôte) et
// l'hôte se rapproche tout seul ; ici l'opérateur dit ce qui est vrai quand la
// déduction se trompe, par exemple un VPS dont l'hébergeur est inconnu. Vider
// un champ rend la main à la déduction.
export function PlacementMachine() {
  const { id = "" } = useParams();
  const machines = useMachines();
  const current = machines.data?.machines.find((machine) => machine.id === id);
  if (machines.data && !current) {
    return <Navigate to="/machines" replace />;
  }
  return current && machines.data ? <PlacementDialog machine={current} all={machines.data.machines} /> : null;
}

function PlacementDialog({ machine, all }: { machine: Machine; all: Machine[] }) {
  const t = useT();
  const navigate = useNavigate();
  const action = useAction();
  const formID = useId();
  const initial = placementOf(machine);
  const [values, setValues] = useState<PlacementValues>(initial);
  const close = () => void navigate(`/machines/${machine.id}`, { replace: true });

  const save = (event: FormEvent) => {
    event.preventDefault();
    action.run(() => setMachinePlacement(machine.id, values.kindOverride, values.hostOverride), { topic: "machines", onDone: close });
  };

  const kindOptions = [
    { value: "", label: t("machine.placement_kind_deduced") },
    ...kindIDs.map((kind) => ({ value: kind, label: t(`machine.kind_${kind}`) })),
  ];
  return (
    <Modal
      title={t("machine.placement_title", machine.name)}
      onClose={close}
      dirty={isPlacementModified(values, initial)}
      actions={
        <>
          <ModalCancel />
          <Button type="submit" form={formID} variant="primary" icon="check" disabled={action.busy || !isPlacementModified(values, initial)}>
            {t("machine.placement_save")}
          </Button>
        </>
      }
    >
      <form id={formID} className="modal-form" onSubmit={save}>
        <p className="secondary">{t("machine.placement_text")}</p>
        <Field label={t("machine.placement_kind")} htmlFor="placement-kind" help={t("machine.placement_kind_help")}>
          <Select id="placement-kind" value={values.kindOverride} options={kindOptions} onChange={(kind) => setValues({ ...values, kindOverride: kind as KindID | "" })} />
        </Field>
        <Field label={t("machine.placement_host")} htmlFor="placement-host" help={t("machine.placement_host_help")}>
          <Select
            id="placement-host"
            value={values.hostOverride}
            options={hostOptions(t("machine.placement_host_none"), hostCandidates(machine, all))}
            onChange={(host) => setValues({ ...values, hostOverride: host })}
          />
        </Field>
        <Failure error={action.error} />
      </form>
    </Modal>
  );
}
