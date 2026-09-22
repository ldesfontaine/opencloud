import { useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { api, errorKey } from "../api/client";
import type { AlertKind, AlertObjectKind, JobsResponse, MachinesResponse, ProbesResponse, ServicesResponse, Silence } from "../api/types";
import { alertKinds } from "../api/types";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { silenceDurations } from "../lib/alerts";
import { useRefresh } from "../lib/refresh";
import "./StatusAdmin.scss";

type PickKind = AlertObjectKind | "";

const objectKinds: readonly PickKind[] = ["", "machine", "service", "heartbeat", "probe"];

// Faire taire : un type d'alerte, un objet, ou les deux, pendant une
// durée. Venue d'une alerte, la page arrive pré-remplie sur son objet.
export function AlertSilenceNew() {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [params] = useSearchParams();
  const [kind, setKind] = useState<AlertKind | "">(asKind(params.get("kind")));
  const [objectKind, setObjectKind] = useState<PickKind>(asObjectKind(params.get("object_kind")));
  const [objectID, setObjectID] = useState(params.get("object_id") ?? "");
  const [objectName, setObjectName] = useState(params.get("object_name") ?? "");
  const [minutes, setMinutes] = useState(60);
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const options = useObjects(objectKind);

  const pickKind = (next: PickKind) => {
    setObjectKind(next);
    setObjectID("");
    setObjectName("");
  };
  const pickObject = (id: string) => {
    setObjectID(id);
    setObjectName(options.find((option) => option.id === id)?.name ?? "");
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    api
      .post<Silence>("/api/alerts/silences", {
        kind,
        object_kind: objectID !== "" ? objectKind : "",
        object_id: objectID,
        object_name: objectName,
        reason,
        duration_minutes: minutes,
      })
      .then(() => {
        refresh("alerts");
        void navigate("/alertes/silences");
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <PageHead title={t("silence.new_title")} subtitle={t("silence.new_subtitle")} />
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("silence.kind_label")} htmlFor="silence-kind">
            <Select id="silence-kind" value={kind} onChange={(event) => setKind(asKind(event.target.value))}>
              <option value="">{t("silence.kind_any")}</option>
              {alertKinds.map((candidate) => (
                <option key={candidate} value={candidate}>
                  {t(`alert.fact_${candidate}`, 0)}
                </option>
              ))}
            </Select>
          </Field>
          <div className="grid-2">
            <Field label={t("silence.object_kind_label")} htmlFor="silence-object-kind">
              <Select id="silence-object-kind" value={objectKind} onChange={(event) => pickKind(asObjectKind(event.target.value))}>
                {objectKinds.map((candidate) => (
                  <option key={candidate} value={candidate}>
                    {candidate === "" ? t("silence.object_kind_none") : t(`silence.object_kind_${candidate}`)}
                  </option>
                ))}
              </Select>
            </Field>
            {objectKind !== "" && objectKind !== "volume" && (
              <Field label={t("silence.object_label")} htmlFor="silence-object">
                <Select id="silence-object" value={objectID} onChange={(event) => pickObject(event.target.value)}>
                  <option value="">—</option>
                  {options.map((option) => (
                    <option key={option.id} value={option.id}>
                      {option.name}
                    </option>
                  ))}
                </Select>
              </Field>
            )}
            {objectKind === "volume" && (
              <Field label={t("silence.object_label")} htmlFor="silence-volume">
                <Input id="silence-volume" mono type="text" value={objectName} readOnly />
              </Field>
            )}
          </div>
          <div className="grid-2">
            <Field label={t("silence.duration_label")} htmlFor="silence-duration">
              <Select id="silence-duration" value={minutes} onChange={(event) => setMinutes(Number(event.target.value))}>
                {silenceDurations.map((duration) => (
                  <option key={duration.key} value={duration.minutes}>
                    {t(`silence.duration_${duration.key}`)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={t("silence.reason_label")} htmlFor="silence-reason" help={t("silence.reason_help")}>
              <Input id="silence-reason" type="text" value={reason} onChange={(event) => setReason(event.target.value)} maxLength={200} autoComplete="off" />
            </Field>
          </div>
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon="pause" disabled={busy || (kind === "" && objectID === "")}>
              {t("silence.create")}
            </Button>
            <Button variant="ghost" to="/alertes/silences">
              {t("silence.cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}

function asKind(value: string | null): AlertKind | "" {
  return alertKinds.find((candidate) => candidate === value) ?? "";
}

function asObjectKind(value: string | null): PickKind {
  if (value === "volume") {
    return "volume";
  }
  return objectKinds.find((candidate) => candidate === value) ?? "";
}

interface Option {
  id: string;
  name: string;
}

// Les objets d'un genre, à choisir par leur nom ; une liste par genre,
// lue seulement quand le genre est choisi.
function useObjects(kind: PickKind): Option[] {
  const machines = useResource<MachinesResponse>(kind === "machine" ? "/api/machines" : null);
  const services = useResource<ServicesResponse>(kind === "service" ? "/api/services" : null);
  const jobs = useResource<JobsResponse>(kind === "heartbeat" ? "/api/jobs" : null);
  const probes = useResource<ProbesResponse>(kind === "probe" ? "/api/probes" : null);
  switch (kind) {
    case "machine":
      return (machines.data?.machines ?? []).map((machine) => ({ id: machine.id, name: machine.name }));
    case "service":
      return (services.data?.services ?? []).map((service) => ({ id: service.id, name: `${service.name} · ${service.machine_name}` }));
    case "heartbeat":
      return (jobs.data?.jobs ?? []).map((job) => ({ id: job.id, name: job.name }));
    case "probe":
      return (probes.data?.probes ?? []).map((probe) => ({ id: probe.id, name: probe.name }));
  }
  return [];
}
