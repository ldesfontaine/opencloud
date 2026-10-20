import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { alertKinds, createSilence, type AlertKind } from "../api/alerts";
import { useContainers } from "../api/containers";
import { useJobs } from "../api/jobs";
import { useMachines } from "../api/machines";
import { useProbes } from "../api/probes";
import { Field, Input, Select } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { PageHead } from "../components/PageHead";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { emptySilenceForm, hasSilenceTarget, silenceDurations, silenceRequestOf, type SilenceObjectKind } from "../lib/alerts";
import { nameWithMachine } from "../lib/objects";

const objectKinds: readonly SilenceObjectKind[] = ["", "machine", "service", "heartbeat", "probe"];

// Faire taire : un type d'alerte, un objet, ou les deux, pendant une
// durée. Venue d'une alerte, la page arrive pré-remplie sur son objet.
export function AlertSilenceNew() {
  const t = useT();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const form = useForm({
    ...emptySilenceForm,
    kind: asKind(params.get("kind")),
    objectKind: asObjectKind(params.get("object_kind")),
    objectID: params.get("object_id") ?? "",
    objectName: params.get("object_name") ?? "",
  });
  const { values, change } = form;
  const action = useAction();
  const options = useObjects(values.objectKind);
  // Un volume ne se choisit pas dans une liste : il vient de l'alerte. On
  // le garde pour y revenir après avoir regardé un autre objet.
  const [alertVolume] = useState(values.objectKind === "volume" ? { id: values.objectID, name: values.objectName } : null);

  const pickKind = (next: SilenceObjectKind) => {
    const volume = next === "volume" ? alertVolume : null;
    change("objectKind", next);
    change("objectID", volume?.id ?? "");
    change("objectName", volume?.name ?? "");
  };
  const pickObject = (id: string) => {
    change("objectID", id);
    change("objectName", options.find((option) => option.id === id)?.name ?? "");
  };

  const save = () => {
    const body = silenceRequestOf(values);
    action.run(() => createSilence(body), {
      topic: "alerts",
      onDone: () => void navigate("/alertes/silences"),
    });
  };

  return (
    <>
      <PageHead title={t("silence.new_title")} subtitle={t("silence.new_subtitle")} />
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={t("silence.create")}
            submitIcon="pause"
            busy={action.busy}
            canSubmit={hasSilenceTarget(values)}
            cancelLabel={t("silence.cancel")}
            cancelTo="/alertes/silences"
          />
        }
      >
        {/* Des Select et non des EnumSelect : l'option vide a sa propre clé, et la durée est un nombre. */}
        <Field label={t("silence.kind_label")} htmlFor="silence-kind">
          <Select id="silence-kind" value={values.kind} onChange={(event) => change("kind", asKind(event.target.value))}>
            <option value="">{t("silence.kind_any")}</option>
            {alertKinds.map((candidate) => (
              <option key={candidate} value={candidate}>
                {t(`alert.kind_${candidate}`)}
              </option>
            ))}
          </Select>
        </Field>
        <div className="grid-2">
          <Field label={t("silence.object_kind_label")} htmlFor="silence-object-kind">
            <Select id="silence-object-kind" value={values.objectKind} onChange={(event) => pickKind(asObjectKind(event.target.value))}>
              {(alertVolume !== null ? [...objectKinds, "volume" as const] : objectKinds).map((candidate) => (
                <option key={candidate} value={candidate}>
                  {candidate === "" ? t("silence.object_kind_none") : t(`silence.object_kind_${candidate}`)}
                </option>
              ))}
            </Select>
          </Field>
          {values.objectKind !== "" && values.objectKind !== "volume" && (
            <Field label={t("silence.object_label")} htmlFor="silence-object">
              <Select id="silence-object" value={values.objectID} onChange={(event) => pickObject(event.target.value)}>
                <option value="">—</option>
                {options.map((option) => (
                  <option key={option.id} value={option.id}>
                    {option.name}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          {values.objectKind === "volume" && (
            <Field label={t("silence.object_label")} htmlFor="silence-volume">
              <Input id="silence-volume" mono type="text" value={values.objectName} readOnly />
            </Field>
          )}
        </div>
        <div className="grid-2">
          <Field label={t("silence.duration_label")} htmlFor="silence-duration">
            <Select id="silence-duration" value={values.minutes} onChange={(event) => change("minutes", Number(event.target.value))}>
              {silenceDurations.map((duration) => (
                <option key={duration.key} value={duration.minutes}>
                  {t(`silence.duration_${duration.key}`)}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t("silence.reason_label")} htmlFor="silence-reason" help={t("silence.reason_help")}>
            <Input id="silence-reason" type="text" value={values.reason} onChange={(event) => change("reason", event.target.value)} maxLength={200} autoComplete="off" />
          </Field>
        </div>
      </FormCard>
    </>
  );
}

function asKind(value: string | null): AlertKind | "" {
  return alertKinds.find((candidate) => candidate === value) ?? "";
}

function asObjectKind(value: string | null): SilenceObjectKind {
  if (value === "volume") {
    return "volume";
  }
  return objectKinds.find((candidate) => candidate === value) ?? "";
}

interface Option {
  id: string;
  name: string;
}

// Les objets d'un genre, à choisir par leur nom et leur machine ; une liste par genre,
// lue seulement quand le genre est choisi.
function useObjects(kind: SilenceObjectKind): Option[] {
  const machines = useMachines(kind === "machine");
  const containers = useContainers(kind === "service");
  const jobs = useJobs(kind === "heartbeat");
  const probes = useProbes(kind === "probe");
  switch (kind) {
    case "machine":
      return (machines.data?.machines ?? []).map((machine) => ({ id: machine.id, name: machine.name }));
    case "service":
      return (containers.data?.containers ?? []).map((container) => ({ id: container.id, name: nameWithMachine(container) }));
    case "heartbeat":
      return (jobs.data?.jobs ?? []).map((job) => ({ id: job.id, name: nameWithMachine(job) }));
    case "probe":
      return (probes.data?.probes ?? []).map((probe) => ({ id: probe.id, name: nameWithMachine(probe) }));
  }
  return [];
}
