import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { alertKinds, createMute, type AlertKind, type MuteTargetKind } from "../api/alerts";
import { useDeclaredServices } from "../api/directory";
import { useJobs } from "../api/jobs";
import { useMachines } from "../api/machines";
import { useProbes } from "../api/probes";
import { Checkbox, Checks, Field, Input, Select } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { FormPage } from "../components/FormPage";
import { PageHead } from "../components/PageHead";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { canAnnounce, emptyMuteForm, muteRequestOf, silenceDurations } from "../lib/alerts";
import { nameWithMachine } from "../lib/objects";

// Ce qu'on choisit dans une liste ; un conteneur non déclaré ne vient que
// d'une alerte.
const targetKinds: readonly MuteTargetKind[] = ["", "machine", "service", "heartbeat", "probe"];

// Faire taire : un type d'alerte, un objet, les deux, ou tout, pendant une
// durée, tout de suite ou à l'heure dite. Venue d'une alerte, la page arrive
// pré-remplie sur ce qui la porte.
export function AlertMuteNew() {
  const t = useT();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const form = useForm({
    ...emptyMuteForm,
    kind: asKind(params.get("kind")),
    targetKind: asTargetKind(params.get("target_kind")),
    targetID: params.get("target_id") ?? "",
    targetName: params.get("target_name") ?? "",
  });
  const { values, change } = form;
  const announced = values.announce && canAnnounce(values);
  const action = useAction();
  const options = useTargets(values.targetKind);
  // Un conteneur non déclaré ne se choisit pas dans une liste : il vient de
  // l'alerte. On le garde pour y revenir après avoir regardé un autre genre.
  const [alertContainer] = useState(values.targetKind === "container" ? { id: values.targetID, name: values.targetName } : null);

  const pickKind = (next: MuteTargetKind) => {
    const container = next === "container" ? alertContainer : null;
    change("targetKind", next);
    change("targetID", container?.id ?? "");
    change("targetName", container?.name ?? "");
    change("announce", false);
  };

  const save = () => {
    const body = muteRequestOf(values);
    action.run(() => createMute(body), {
      topic: "alerts",
      onDone: () => void navigate("/alertes/silences"),
    });
  };

  return (
    <FormPage>
      <PageHead title={t("silence.new_title")} subtitle={t("silence.new_subtitle")} />
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={t("silence.create")}
            submitIcon="pause"
            busy={action.busy}
            canSubmit={(values.targetKind === "" || values.targetID !== "") && (!announced || values.publicTitle.trim() !== "")}
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
          <Field label={t("silence.target_kind_label")} htmlFor="silence-target-kind">
            <Select id="silence-target-kind" value={values.targetKind} onChange={(event) => pickKind(asTargetKind(event.target.value))}>
              {(alertContainer !== null ? [...targetKinds, "container" as const] : targetKinds).map((candidate) => (
                <option key={candidate} value={candidate}>
                  {candidate === "" ? t("silence.target_kind_none") : t(`silence.target_kind_${candidate}`)}
                </option>
              ))}
            </Select>
          </Field>
          {values.targetKind !== "" && values.targetKind !== "container" && (
            <Field label={t("silence.object_label")} htmlFor="silence-object">
              <Select id="silence-object" value={values.targetID} onChange={(event) => change("targetID", event.target.value)}>
                <option value="">—</option>
                {options.map((option) => (
                  <option key={option.id} value={option.id}>
                    {option.name}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          {values.targetKind === "container" && (
            <Field label={t("silence.object_label")} htmlFor="silence-container">
              <Input id="silence-container" mono type="text" value={values.targetName} readOnly />
            </Field>
          )}
        </div>
        <div className="grid-2">
          <Field label={t("silence.start_label")} htmlFor="silence-start" help={t("silence.start_help")}>
            <Input id="silence-start" mono type="datetime-local" value={values.startsAt} onChange={(event) => change("startsAt", event.target.value)} />
          </Field>
          <Field label={t("silence.duration_label")} htmlFor="silence-duration">
            <Select id="silence-duration" value={values.minutes} onChange={(event) => change("minutes", Number(event.target.value))}>
              {silenceDurations.map((duration) => (
                <option key={duration.key} value={duration.minutes}>
                  {t(`silence.duration_${duration.key}`)}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <Field label={t("silence.reason_label")} htmlFor="silence-reason" help={t("silence.reason_help")}>
          <Input id="silence-reason" type="text" value={values.reason} onChange={(event) => change("reason", event.target.value)} maxLength={200} autoComplete="off" />
        </Field>
        {canAnnounce(values) && (
          <>
            <Checks>
              <Checkbox label={t("silence.announce_label")} checked={values.announce} onChange={(checked) => change("announce", checked)} />
            </Checks>
            <p className="secondary">{t("silence.announce_help")}</p>
            {values.announce && (
              <Field label={t("silence.public_title_label")} htmlFor="silence-public-title">
                <Input id="silence-public-title" type="text" value={values.publicTitle} onChange={(event) => change("publicTitle", event.target.value)} maxLength={120} autoComplete="off" />
              </Field>
            )}
          </>
        )}
      </FormCard>
    </FormPage>
  );
}

function asKind(value: string | null): AlertKind | "" {
  return alertKinds.find((candidate) => candidate === value) ?? "";
}

function asTargetKind(value: string | null): MuteTargetKind {
  if (value === "container") {
    return "container";
  }
  return targetKinds.find((candidate) => candidate === value) ?? "";
}

interface Option {
  id: string;
  name: string;
}

// Les objets d'un genre, à choisir par leur nom et leur machine ; une liste par genre,
// lue seulement quand le genre est choisi.
function useTargets(kind: MuteTargetKind): Option[] {
  const machines = useMachines(kind === "machine");
  const services = useDeclaredServices(kind === "service");
  const jobs = useJobs(kind === "heartbeat");
  const probes = useProbes(kind === "probe");
  switch (kind) {
    case "machine":
      return (machines.data?.machines ?? []).map((machine) => ({ id: machine.id, name: machine.name }));
    case "service":
      return (services.data?.services ?? []).map((service) => ({ id: service.id, name: nameWithMachine({ name: service.title !== "" ? service.title : service.name, machine_name: service.machine_name }) }));
    case "heartbeat":
      return (jobs.data?.jobs ?? []).map((job) => ({ id: job.id, name: nameWithMachine(job) }));
    case "probe":
      return (probes.data?.probes ?? []).map((probe) => ({ id: probe.id, name: nameWithMachine(probe) }));
  }
  return [];
}
