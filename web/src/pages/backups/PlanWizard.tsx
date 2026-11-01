import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";

import { saveBackupPlan, useBackups, type BackupPlan, type Backups } from "../../api/backups";
import { useMachines } from "../../api/machines";
import { useMachineProxmox } from "../../api/proxmox";
import { Failure } from "../../components/Failure";
import { Field, Input } from "../../components/Field";
import { MachineSelect } from "../../components/MachineSelect";
import { Modal } from "../../components/Modal";
import { EnumSelect, Select } from "../../components/Select";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT, type Translate } from "../../i18n/context";
import { backupsPath, emptyPlanForm, unfitDestination, planFormOf, planRequestOf, planTitle, producers, sourceKinds, sourceKindsFor, subjectProblem, type PlanFormValues } from "../../lib/backups";
import { isFormModified } from "../../lib/modal";
import { unfitFor } from "../../lib/rights";
import { GuestSelector } from "./GuestSelector";

type PlanForm = ReturnType<typeof useForm<PlanFormValues>>;

// « Ajouter un plan » : la fenêtre de la page Sauvegardes, à
// /sauvegardes/plans/nouveau, ou /sauvegardes/plans/:subject/modifier pour
// celui qu'on corrige. La fenêtre attend le relevé des sauvegardes : elle y lit
// les destinations et les plans.
export function PlanWizard() {
  const { subject } = useParams();
  const backups = useBackups();
  if (backups.data === null) {
    return null;
  }
  const editing = subject === undefined ? null : (backups.data.plans.find((plan) => plan.subject === subject) ?? null);
  if (subject !== undefined && editing === null) {
    return <Navigate to={backupsPath} replace />;
  }
  return <PlanDialog overview={backups.data} editing={editing} />;
}

// Le sujet et la sorte de source sont l'identité du plan, ils ne se changent
// pas ; le reste se règle. Deux étapes : d'où part la sauvegarde, puis où elle
// va et combien en garder.
function PlanDialog({ overview, editing }: { overview: Backups; editing: BackupPlan | null }) {
  const t = useT();
  const navigate = useNavigate();
  const hasDatabasePlan = overview.plans.some((plan) => plan.source_kind === "database");
  // Un plan neuf part de la première destination qui convient ; la saisie se compare à
  // l'état d'ouverture, pas à ce que le direct relit ensuite.
  const [initial] = useState(() => (editing === null ? { ...emptyPlanForm(hasDatabasePlan), destination: overview.destinations.find((candidate) => unfitDestination(t, candidate) === null)?.id ?? "" } : planFormOf(editing)));
  const form = useForm(initial);
  const { values } = form;
  const action = useAction();
  const isEditing = editing !== null;

  const close = () => void navigate(backupsPath, { replace: true });
  const save = () => {
    const { subject, request } = planRequestOf(values, values.destination);
    action.run(() => saveBackupPlan(subject, request), { topic: "backups", onDone: close });
  };

  return (
    <Modal
      title={isEditing ? t("backup.plan_edit_title", planTitle(editing, t("backup.source_kind_database"))) : t("backup.plan_add_title")}
      onClose={close}
      dirty={isFormModified(values, initial)}
      wide
    >
      <Wizard
        steps={stepsOf(t, form, overview, isEditing, hasDatabasePlan)}
        mode={isEditing ? "edit" : "create"}
        onSubmit={save}
        submitLabel={isEditing ? t("backup.plan_save") : t("backup.plan_add")}
        submitIcon={isEditing ? "check" : "plus"}
        busy={action.busy}
        error={action.error}
        errorGesture={values.kind === "agent" && values.machine !== "" ? { machineID: values.machine } : undefined}
      />
    </Modal>
  );
}

function stepsOf(t: Translate, form: PlanForm, overview: Backups, isEditing: boolean, hasDatabasePlan: boolean): WizardStep[] {
  const { values, change } = form;
  const isAgent = values.kind === "agent";
  return [
    {
      id: "source",
      title: t("backup.plan_step_source"),
      validate: () => {
        if (isAgent && values.machine === "") {
          return "backup.plan_machine_missing";
        }
        if (isAgent && values.selector.trim() === "") {
          return "backup.plan_selector_missing";
        }
        return subjectProblem(values, isEditing ? [] : overview.plans.map((plan) => plan.subject));
      },
      render: () => (
        <>
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
          {isAgent && <AgentSource form={form} />}
        </>
      ),
      summary: () => [
        { label: t("backup.plan_source"), value: t(`backup.source_kind_${values.kind}`) },
        ...(isAgent
          ? [
              { label: t("backup.plan_subject"), value: values.subject, mono: true },
              { label: t("backup.plan_producer"), value: t(`backup.producer_${values.producer}`) },
              { label: t("backup.plan_selector"), value: values.selector, mono: true },
            ]
          : []),
        { label: t("backup.plan_name"), value: values.name === "" ? "—" : values.name },
      ],
    },
    {
      id: "destination",
      title: t("backup.plan_step_destination"),
      validate: () => (values.destination !== "" ? null : overview.destinations.length === 0 ? "backup.plan_destination_missing" : "backup.plan_destination_unfit"),
      render: () => (
        <>
          <Field label={t("backup.plan_destination_label")} htmlFor="backup-plan-destination">
            <Select
              id="backup-plan-destination"
              value={values.destination}
              options={overview.destinations.map((candidate) => {
                const unfit = unfitDestination(t, candidate);
                return unfit === null ? { value: candidate.id, label: candidate.name } : { value: candidate.id, label: candidate.name, disabled: true, reason: unfit.reason, link: unfit.link };
              })}
              onChange={(chosen) => change("destination", chosen)}
            />
          </Field>
          <Field label={t("backup.plan_keep_label")} htmlFor="backup-plan-keep" help={t("backup.plan_keep_help")}>
            <Input id="backup-plan-keep" type="number" inputMode="numeric" min={1} max={365} value={values.keep} onChange={(event) => change("keep", event.target.value)} required />
          </Field>
          <p className="secondary">{t("backup.schedule_daily")}</p>
        </>
      ),
      summary: () => [
        { label: t("backup.plan_destination_label"), value: overview.destinations.find((candidate) => candidate.id === values.destination)?.name ?? "—" },
        { label: t("backup.plan_keep_label"), value: values.keep, mono: true },
      ],
    },
  ];
}

// Ce que demande une source agent : la machine qui produit, le producteur, et
// ce qu'il en prend. Sur un hôte Proxmox déclaré, avec son relevé, le choix se
// fait dans la liste de ses invités ; sinon le sélecteur se saisit.
function AgentSource({ form }: { form: PlanForm }) {
  const t = useT();
  const machines = useMachines();
  const { values, change } = form;
  const sourceMachine = machines.data?.machines.find((candidate) => candidate.id === values.machine);
  const host = useMachineProxmox(values.machine, sourceMachine?.proxmox.declared === true);
  const guests = host.data?.guests ?? [];
  const hasGuests = guests.some((guest) => !guest.template);
  return (
    <>
      <Failure error={machines.error} />
      <Field label={t("backup.plan_machine")} htmlFor="backup-plan-machine" help={t("backup.plan_machine_help")}>
        <MachineSelect id="backup-plan-machine" machines={machines.data?.machines ?? []} fit={(machine) => unfitFor(t, machine, "backup.produce")} value={values.machine} onChange={(id) => change("machine", id)} noneLabel={t("backup.destination_machine_choose")} />
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
  );
}
