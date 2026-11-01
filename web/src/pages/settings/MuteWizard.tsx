import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { alertKinds, createMute, type AlertKind, type MuteTargetKind } from "../../api/alerts";
import { useDeclaredServices } from "../../api/directory";
import { useJobs } from "../../api/jobs";
import { useMachines } from "../../api/machines";
import { useProbes } from "../../api/probes";
import { SettingsFold } from "../../components/Disclosure";
import { Checkbox, Checks, Field, Input } from "../../components/Field";
import { Modal } from "../../components/Modal";
import { Select } from "../../components/Select";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT, type Translate } from "../../i18n/context";
import { canAnnounce, emptyMuteForm, muteRequestOf, muteSettingsHint, silenceDurations } from "../../lib/alerts";
import { isFormModified } from "../../lib/modal";
import { nameWithMachine } from "../../lib/objects";

// Ce qu'on choisit dans une liste ; un conteneur non déclaré ne vient que
// d'une alerte.
const targetKinds: readonly MuteTargetKind[] = ["", "machine", "service", "heartbeat", "probe"];

const settingsPath = "/parametres";

// Faire taire : la fenêtre de Paramètres › Notifications, à
// /parametres/silences/nouveau. Un seul écran : l'objet visé (ou tout), la
// durée, et l'annonce publique quand l'objet le permet. Le type d'alerte, le
// début et le motif sont repliés sous « Réglages ». Venue d'une alerte ou d'un
// service, la fenêtre arrive pré-remplie sur ce qui la porte.
export function MuteWizard() {
  const t = useT();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [initial] = useState(() => ({
    ...emptyMuteForm,
    kind: asKind(params.get("kind")),
    targetKind: asTargetKind(params.get("target_kind")),
    targetID: params.get("target_id") ?? "",
    targetName: params.get("target_name") ?? "",
  }));
  const form = useForm(initial);
  const { values } = form;
  const action = useAction();
  const options = useTargets(values.targetKind);
  // Un conteneur non déclaré ne se choisit pas dans une liste : il vient de
  // l'alerte. On le garde pour y revenir après avoir regardé un autre genre.
  const alertContainer = initial.targetKind === "container" ? { id: initial.targetID, name: initial.targetName } : null;

  const close = () => void navigate(settingsPath, { replace: true });
  const save = () => {
    const body = muteRequestOf(values);
    action.run(() => createMute(body), { topic: "alerts", onDone: close });
  };

  return (
    <Modal title={t("silence.new_title")} onClose={close} dirty={isFormModified(values, initial)} wide>
      <p className="secondary">{t("silence.new_subtitle")}</p>
      <Wizard steps={stepsOf(t, form, options, alertContainer)} mode="create" onSubmit={save} submitLabel={t("silence.create")} submitIcon="pause" busy={action.busy} error={action.error} />
    </Modal>
  );
}

interface TargetOption {
  id: string;
  name: string;
}

function stepsOf(t: Translate, form: ReturnType<typeof useForm<typeof emptyMuteForm>>, options: TargetOption[], alertContainer: TargetOption | null): WizardStep[] {
  const { values, change } = form;
  const announced = values.announce && canAnnounce(values);
  const pickKind = (next: MuteTargetKind) => {
    const container = next === "container" ? alertContainer : null;
    change("targetKind", next);
    change("targetID", container?.id ?? "");
    change("targetName", container?.name ?? "");
    change("announce", false);
  };
  return [
    {
      id: "mute",
      title: t("silence.step_mute"),
      // Choisir un genre sans en choisir l'objet ne vise rien ; une annonce sans titre ne dit rien : on le dit ici.
      validate: () => {
        if (values.targetKind !== "" && values.targetID === "") {
          return "silence.object_missing";
        }
        return announced && values.publicTitle.trim() === "" ? "silence.public_title_missing" : null;
      },
      render: () => (
        <>
          {/* Des Select et non des EnumSelect : l'option vide a sa propre clé, et la durée est un nombre. */}
          <div className="grid-2">
            <Field label={t("silence.target_kind_label")} htmlFor="silence-target-kind">
              <Select
                id="silence-target-kind"
                value={values.targetKind}
                options={(alertContainer !== null ? [...targetKinds, "container" as const] : targetKinds).map((candidate) => ({
                  value: candidate,
                  label: candidate === "" ? t("silence.target_kind_none") : t(`silence.target_kind_${candidate}`),
                }))}
                onChange={(chosen) => pickKind(asTargetKind(chosen))}
              />
            </Field>
            {values.targetKind !== "" && values.targetKind !== "container" && (
              <Field label={t("silence.object_label")} htmlFor="silence-object">
                <Select
                  id="silence-object"
                  value={values.targetID}
                  options={[{ value: "", label: "—" }, ...options.map((option) => ({ value: option.id, label: option.name }))]}
                  onChange={(chosen) => change("targetID", chosen)}
                />
              </Field>
            )}
            {values.targetKind === "container" && (
              <Field label={t("silence.object_label")} htmlFor="silence-container">
                <Input id="silence-container" mono type="text" value={values.targetName} readOnly />
              </Field>
            )}
          </div>
          <Field label={t("silence.duration_label")} htmlFor="silence-duration">
            <Select
              id="silence-duration"
              value={String(values.minutes)}
              options={silenceDurations.map((duration) => ({ value: String(duration.minutes), label: t(`silence.duration_${duration.key}`) }))}
              onChange={(chosen) => change("minutes", Number(chosen))}
            />
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
          <SettingsFold hint={muteSettingsHint(t, values)} initiallyOpen={values.kind !== ""}>
            <Field label={t("silence.kind_label")} htmlFor="silence-kind">
              <Select
                id="silence-kind"
                value={values.kind}
                options={[{ value: "", label: t("silence.kind_any") }, ...alertKinds.map((candidate) => ({ value: candidate, label: t(`alert.kind_${candidate}`) }))]}
                onChange={(chosen) => change("kind", asKind(chosen))}
              />
            </Field>
            <Field label={t("silence.start_label")} htmlFor="silence-start" help={t("silence.start_help")}>
              <Input id="silence-start" mono type="datetime-local" value={values.startsAt} onChange={(event) => change("startsAt", event.target.value)} />
            </Field>
            <Field label={t("silence.reason_label")} htmlFor="silence-reason" help={t("silence.reason_help")}>
              <Input id="silence-reason" type="text" value={values.reason} onChange={(event) => change("reason", event.target.value)} maxLength={200} autoComplete="off" />
            </Field>
          </SettingsFold>
        </>
      ),
    },
  ];
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
