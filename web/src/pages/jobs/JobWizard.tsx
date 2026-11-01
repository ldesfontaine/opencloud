import { useNavigate, useParams } from "react-router";

import { saveJob, useJob, type JobResponse } from "../../api/jobs";
import { useMachines, type Machine } from "../../api/machines";
import { Note } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Field, Input } from "../../components/Field";
import { MachineSelect } from "../../components/MachineSelect";
import { Modal } from "../../components/Modal";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT, type Translate } from "../../i18n/context";
import { emptyJobForm, jobFormOf, jobRequestOf, type JobFormValues } from "../../lib/jobs";
import { isFormModified } from "../../lib/modal";

// « Surveiller une tâche » : la fenêtre de la liste des tâches, à l'adresse
// /taches/nouvelle, ou de la fiche d'une tâche, à /taches/:id/modifier. Une
// étape (le nom, la machine, tous les combien elle doit pinger) puis le
// récapitulatif. En édition, l'assistant s'ouvre sur le récapitulatif ; il ne
// touche ni l'identifiant ni le jeton. Le serveur valide et répond par la clé
// du refus.
export function JobWizard() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const navigate = useNavigate();
  const machines = useMachines();
  const existing = useJob(id ?? null);
  const form = useForm(emptyJobForm, isEditing ? { source: existing.data, fill: (response: JobResponse) => jobFormOf(response.job) } : null);
  const { values } = form;
  const action = useAction();

  const close = () => void navigate(isEditing ? `/taches/${id}` : "/taches", { replace: true });
  const save = () => {
    const body = jobRequestOf(values);
    action.run(() => saveJob(id, body), {
      topic: "all",
      onDone: (saved) => void navigate(`/taches/${saved.id}`, { replace: true }),
    });
  };

  const base = isEditing && existing.data !== null ? jobFormOf(existing.data.job) : emptyJobForm;
  return (
    <Modal title={isEditing ? t("job.edit_title") : t("job.new_title")} onClose={close} dirty={form.isFilled && isFormModified(values, base)} wide>
      <p className="secondary">{isEditing ? t("job.edit_subtitle") : t("job.new_subtitle")}</p>
      {isEditing && <Note tone="warn">{t("job.edit_note")}</Note>}
      {existing.error && <Failure error={existing.error} />}
      {form.isFilled && (
        <Wizard
          steps={stepsOf(t, form, machines.data?.machines ?? [])}
          mode={isEditing ? "edit" : "create"}
          onSubmit={save}
          submitLabel={isEditing ? t("job.save") : t("job.create")}
          submitIcon={isEditing ? "check" : "plus"}
          busy={action.busy}
          error={action.error}
        />
      )}
    </Modal>
  );
}

function stepsOf(t: Translate, form: ReturnType<typeof useForm<JobFormValues, JobResponse>>, machines: Pick<Machine, "id" | "name" | "kind">[]): WizardStep[] {
  const { values, change } = form;
  return [
    {
      id: "job",
      title: t("job.step_job"),
      render: () => (
        <>
          <Field label={t("job.name_label")} htmlFor="job-name" help={t("job.name_help")}>
            <Input
              id="job-name"
              type="text"
              value={values.name}
              onChange={(event) => change("name", event.target.value)}
              placeholder={t("job.name_placeholder")}
              maxLength={80}
              autoComplete="off"
              autoFocus
              required
            />
          </Field>
          <Field label={t("job.machine_label")} htmlFor="job-machine" help={t("job.machine_help")}>
            <MachineSelect id="job-machine" machines={machines} value={values.machineID} onChange={(machineID) => change("machineID", machineID)} noneLabel={t("job.machine_none")} />
          </Field>
          <div className="grid-2">
            <Field label={t("job.interval_label")} htmlFor="job-interval" help={t("job.interval_help")}>
              <Input id="job-interval" mono type="number" value={values.interval} onChange={(event) => change("interval", event.target.value)} min={1} max={10080} required />
            </Field>
            <Field label={t("job.grace_label")} htmlFor="job-grace" help={t("job.grace_help")}>
              <Input id="job-grace" mono type="number" value={values.grace} onChange={(event) => change("grace", event.target.value)} min={0} max={10080} required />
            </Field>
          </div>
        </>
      ),
      summary: () => [
        { label: t("job.name_label"), value: values.name },
        { label: t("job.machine_label"), value: machines.find((machine) => machine.id === values.machineID)?.name ?? t("job.machine_none") },
        { label: t("job.interval_label"), value: values.interval, mono: true },
        { label: t("job.grace_label"), value: values.grace, mono: true },
      ],
    },
  ];
}
