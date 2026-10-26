import { useEffect, useRef, useState } from "react";

import { saveBackupPlan, type BackupPlan, type Backups } from "../../api/backups";
import { useMachines } from "../../api/machines";
import { useMachineProxmox } from "../../api/proxmox";
import { CardHeader } from "../../components/Card";
import { Field, Input } from "../../components/Field";
import { FormActions, FormCard } from "../../components/FormCard";
import { MachineSelect } from "../../components/MachineSelect";
import { EnumSelect, Select } from "../../components/Select";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { emptyPlanForm, planFormOf, planRequestOf, planTitle, producers, sourceKinds, sourceKindsFor, subjectProblem } from "../../lib/backups";
import { GuestSelector } from "./GuestSelector";

// Le formulaire d'un plan : un neuf, ou celui qu'on modifie. Le sujet et la
// sorte de source sont l'identité du plan, ils ne se changent pas ; le reste
// se règle.
export function PlanForm({ overview, editing, onDone }: { overview: Backups; editing: BackupPlan | null; onDone: () => void }) {
  const t = useT();
  const machines = useMachines();
  const hasDatabasePlan = overview.plans.some((plan) => plan.source_kind === "database");
  const form = useForm(editing === null ? emptyPlanForm(hasDatabasePlan) : planFormOf(editing));
  const { values, change, update } = form;
  const action = useAction();
  const [refusal, setRefusal] = useState<string | null>(null);
  const card = useRef<HTMLDivElement>(null);
  const isEditing = editing !== null;
  const isAgent = values.kind === "agent";
  const destination = values.destination === "" ? (overview.destinations[0]?.id ?? "") : values.destination;
  // Un hôte Proxmox déclaré sur la machine source, avec son relevé : le choix
  // se fait dans la liste de ses invités. Sinon, le sélecteur se saisit.
  const sourceMachine = machines.data?.machines.find((candidate) => candidate.id === values.machine);
  const host = useMachineProxmox(values.machine, isAgent && sourceMachine?.proxmox.declared === true);
  const guests = host.data?.guests ?? [];
  const hasGuests = guests.some((guest) => !guest.template);
  const isComplete = destination !== "" && (!isAgent || (values.subject !== "" && values.machine !== "" && values.selector !== ""));

  // « Modifier » sur un plan amène ici : le formulaire est sous la liste.
  useEffect(() => {
    if (isEditing) {
      card.current?.scrollIntoView?.({ block: "nearest" });
    }
  }, [isEditing]);

  const save = () => {
    const problem = subjectProblem(values, isEditing ? [] : overview.plans.map((plan) => plan.subject));
    setRefusal(problem);
    if (problem !== null) {
      return;
    }
    const { subject, request } = planRequestOf(values, destination);
    action.run(() => saveBackupPlan(subject, request), {
      topic: "backups",
      onDone: () => {
        update(emptyPlanForm(hasDatabasePlan || values.kind === "database"));
        onDone();
      },
    });
  };
  return (
    <div ref={card}>
      <FormCard
        width="fill"
        onSubmit={save}
        error={refusal ?? action.error}
        header={<CardHeader title={isEditing ? t("backup.plan_edit_title", planTitle(editing, t("backup.source_kind_database"))) : t("backup.plan_add_title")} />}
        actions={
          <FormActions
            submitLabel={isEditing ? t("backup.plan_save") : t("backup.plan_add")}
            submitIcon={isEditing ? "check" : "plus"}
            busy={action.busy}
            canSubmit={isComplete}
            cancelLabel={t("backup.plan_cancel")}
            {...(isEditing && { onCancel: onDone })}
          />
        }
      >
        <Field label={t("backup.plan_source")} htmlFor="backup-plan-kind">
          <EnumSelect id="backup-plan-kind" value={values.kind} values={isEditing ? sourceKinds : sourceKindsFor(hasDatabasePlan)} labelKey="backup.source_kind_" disabled={isEditing} onChange={(kind) => change("kind", kind)} />
        </Field>
        {isAgent && (
          <Field label={t("backup.plan_subject")} htmlFor="backup-plan-subject" help={t("backup.plan_subject_help")}>
            <Input id="backup-plan-subject" mono type="text" autoComplete="off" maxLength={40} readOnly={isEditing} value={values.subject} onChange={(event) => change("subject", event.target.value)} required />
          </Field>
        )}
        <Field label={t("backup.plan_name")} htmlFor="backup-plan-name" help={t("backup.plan_name_help")}>
          <Input id="backup-plan-name" type="text" autoComplete="off" maxLength={80} value={values.name} onChange={(event) => change("name", event.target.value)} />
        </Field>
        {isAgent && (
          <>
            <Field label={t("backup.plan_machine")} htmlFor="backup-plan-machine" help={t("backup.plan_machine_help")}>
              <MachineSelect id="backup-plan-machine" machines={machines.data?.machines ?? []} value={values.machine} onChange={(id) => change("machine", id)} noneLabel={t("backup.destination_machine_choose")} />
            </Field>
            <Field label={t("backup.plan_producer")} htmlFor="backup-plan-producer">
              <EnumSelect id="backup-plan-producer" value={values.producer} values={producers} labelKey="backup.producer_" onChange={(producer) => change("producer", producer)} />
            </Field>
            {hasGuests ? (
              <Field label={t("backup.plan_selector")} htmlFor="backup-plan-selector" help={t("backup.plan_selector_guests_help")}>
                <GuestSelector id="backup-plan-selector" guests={guests} selector={values.selector} onChange={(selector) => change("selector", selector)} />
              </Field>
            ) : (
              <Field label={t("backup.plan_selector")} htmlFor="backup-plan-selector" help={t("backup.plan_selector_help")}>
                <Input id="backup-plan-selector" mono type="text" autoComplete="off" maxLength={255} value={values.selector} onChange={(event) => change("selector", event.target.value)} required />
              </Field>
            )}
          </>
        )}
        <Field label={t("backup.plan_destination_label")} htmlFor="backup-plan-destination">
          <Select
            id="backup-plan-destination"
            value={destination}
            options={overview.destinations.map((candidate) => ({ value: candidate.id, label: candidate.name }))}
            onChange={(chosen) => change("destination", chosen)}
          />
        </Field>
        <Field label={t("backup.plan_keep_label")} htmlFor="backup-plan-keep" help={t("backup.plan_keep_help")}>
          <Input id="backup-plan-keep" type="number" inputMode="numeric" min={1} max={365} value={values.keep} onChange={(event) => change("keep", event.target.value)} required />
        </Field>
        <p className="secondary">{t("backup.schedule_daily")}</p>
      </FormCard>
    </div>
  );
}
