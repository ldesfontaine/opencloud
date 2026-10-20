import { useNavigate, useParams } from "react-router";

import { saveJob, useJob, type JobResponse } from "../api/jobs";
import { useMachines } from "../api/machines";
import { Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { PageHead } from "../components/PageHead";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { emptyJobForm, jobFormOf, jobRequestOf } from "../lib/jobs";
import { NotFound } from "./NotFound";

// Créer ou modifier une tâche : l'opérateur la nomme et dit tous les combien
// elle doit pinger ; le serveur valide et répond par la clé du refus,
// affichée sous le formulaire. L'édition ne touche ni l'identifiant ni le
// jeton.
export function JobForm() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const navigate = useNavigate();
  const machines = useMachines();
  const existing = useJob(id ?? null);
  const form = useForm(emptyJobForm, isEditing ? { source: existing.data, fill: (response: JobResponse) => jobFormOf(response.job) } : null);
  const { values, change } = form;
  const action = useAction();

  if (isEditing && existing.error?.status === 404) {
    return <NotFound />;
  }

  const save = () => {
    const body = jobRequestOf(values);
    action.run(() => saveJob(id, body), {
      topic: "all",
      onDone: (saved) => void navigate(`/taches/${saved.id}`),
    });
  };

  return (
    <>
      <PageHead
        title={isEditing ? t("job.edit_title") : t("job.new_title")}
        subtitle={isEditing ? t("job.edit_subtitle") : t("job.new_subtitle")}
      />
      {existing.error && <Failure error={existing.error} />}
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={isEditing ? t("job.save") : t("job.create")}
            submitIcon={isEditing ? "check" : "plus"}
            busy={action.busy}
            isFilled={form.isFilled}
            cancelLabel={t("job.cancel")}
            cancelTo={isEditing ? `/taches/${id}` : "/taches"}
          />
        }
      >
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
          <Select id="job-machine" value={values.machineID} onChange={(event) => change("machineID", event.target.value)}>
            <option value="">{t("job.machine_none")}</option>
            {(machines.data?.machines ?? []).map((machine) => (
              <option key={machine.id} value={machine.id}>
                {machine.name}
              </option>
            ))}
          </Select>
        </Field>
        <div className="grid-2">
          <Field label={t("job.interval_label")} htmlFor="job-interval" help={t("job.interval_help")}>
            <Input id="job-interval" mono type="number" value={values.interval} onChange={(event) => change("interval", event.target.value)} min={1} max={10080} required />
          </Field>
          <Field label={t("job.grace_label")} htmlFor="job-grace" help={t("job.grace_help")}>
            <Input id="job-grace" mono type="number" value={values.grace} onChange={(event) => change("grace", event.target.value)} min={0} max={10080} required />
          </Field>
        </div>
        {isEditing && <Note tone="warn">{t("job.edit_note")}</Note>}
      </FormCard>
    </>
  );
}
