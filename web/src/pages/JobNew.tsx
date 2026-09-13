import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";

import { api, errorKey } from "../api/client";
import type { Job, MachinesResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { useRefresh } from "../lib/refresh";

// L'opérateur nomme la tâche et dit tous les combien elle doit pinger ; le
// serveur valide et répond par la clé du refus, affichée sous le formulaire.
export function JobNew() {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const machines = useResource<MachinesResponse>("/api/machines");
  const [name, setName] = useState("");
  const [machineID, setMachineID] = useState("");
  const [interval, setInterval] = useState("60");
  const [grace, setGrace] = useState("10");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    api
      .post<Job>("/api/jobs", {
        name,
        machine_id: machineID,
        interval_minutes: Number(interval),
        grace_minutes: Number(grace),
      })
      .then((created) => {
        refresh();
        void navigate(`/taches/${created.id}`);
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <PageHead title={t("job.new_title")} subtitle={t("job.new_subtitle")} />
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("job.name_label")} htmlFor="job-name" help={t("job.name_help")}>
            <Input
              id="job-name"
              type="text"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="sauvegarde nextcloud"
              maxLength={80}
              autoComplete="off"
              autoFocus
              required
            />
          </Field>
          <Field label={t("job.machine_label")} htmlFor="job-machine" help={t("job.machine_help")}>
            <Select id="job-machine" value={machineID} onChange={(event) => setMachineID(event.target.value)}>
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
              <Input id="job-interval" mono type="number" value={interval} onChange={(event) => setInterval(event.target.value)} min={1} max={10080} required />
            </Field>
            <Field label={t("job.grace_label")} htmlFor="job-grace" help={t("job.grace_help")}>
              <Input id="job-grace" mono type="number" value={grace} onChange={(event) => setGrace(event.target.value)} min={0} max={10080} required />
            </Field>
          </div>
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon="plus" disabled={busy}>
              {t("job.create")}
            </Button>
            <Button variant="ghost" to="/taches">
              {t("job.cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
