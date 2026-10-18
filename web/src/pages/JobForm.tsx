import { useEffect, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router";

import { api, errorKey } from "../api/client";
import type { Job, JobResponse, MachinesResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { emptyJobForm, jobFormOf, jobRequestOf, type JobFormValues } from "../lib/jobs";
import { useRefresh } from "../lib/refresh";
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
  const { refresh } = useRefresh();
  const machines = useResource<MachinesResponse>("/api/machines");
  const existing = useResource<JobResponse>(isEditing ? `/api/jobs/${id}` : null);
  const [values, setValues] = useState<JobFormValues>(emptyJobForm);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // La tâche se lit une fois : une relecture ne doit pas écraser ce que
  // l'opérateur est en train de taper.
  const [filled, setFilled] = useState(false);

  const change = <Key extends keyof JobFormValues>(key: Key, value: JobFormValues[Key]) => {
    setValues((current) => ({ ...current, [key]: value }));
  };

  useEffect(() => {
    if (filled || existing.data === null) {
      return;
    }
    setFilled(true);
    setValues(jobFormOf(existing.data.job));
  }, [existing.data, filled]);

  if (isEditing && existing.error?.status === 404) {
    return <NotFound />;
  }

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const body = jobRequestOf(values);
    const request = isEditing ? api.put<Job>(`/api/jobs/${id}`, body) : api.post<Job>("/api/jobs", body);
    request
      .then((saved) => {
        refresh();
        void navigate(`/taches/${saved.id}`);
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <PageHead
        title={isEditing ? t("job.edit_title") : t("job.new_title")}
        subtitle={isEditing ? t("job.edit_subtitle") : t("job.new_subtitle")}
      />
      {existing.error && <Failure error={existing.error} />}
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
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
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon={isEditing ? "check" : "plus"} disabled={busy || (isEditing && !filled)}>
              {isEditing ? t("job.save") : t("job.create")}
            </Button>
            <Button variant="ghost" to={isEditing ? `/taches/${id}` : "/taches"}>
              {t("job.cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
