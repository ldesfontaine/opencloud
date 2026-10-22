import { useEffect, useState } from "react";

import {
  createBackupDestination,
  createKeyring,
  deleteBackupDestination,
  deleteBackupPlan,
  runBackupNow,
  saveBackupPlan,
  useBackups,
  type BackupDestination,
  type BackupRight,
  type Backups as BackupsOverview,
} from "../api/backups";
import { useMachines } from "../api/machines";
import { reasonText } from "../components/ActionsCard";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { Loaded } from "../components/Loaded";
import { MachineSelect } from "../components/MachineSelect";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { Table } from "../components/Table";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { isBackedUp, passphraseProblem } from "../lib/backups";
import type { Tone } from "../lib/tone";
import { formatWhen } from "../lib/time";

const rightTones: Record<BackupRight, Tone> = { ok: "ok", missing: "danger", unknown: "neutral" };
const stateTones: Record<string, Tone> = { sent: "neutral", accepted: "accent", succeeded: "ok", failed: "danger", refused: "danger", expired: "warn", queued: "neutral" };

// La sauvegarde de la base : son état d'abord (est-elle sauvegardée, et où
// en est la dernière), puis la clé, la destination et le plan qui la rendent
// possible. La phrase de passe n'est lue qu'ici, le temps de créer la clé.
export function Backups() {
  const t = useT();
  const backups = useBackups();
  return (
    <>
      <PageHead title={t("backup.title")} subtitle={t("backup.subtitle")} />
      <Loaded resource={backups}>
        {(overview) => (
          <div className="stack">
            {!isBackedUp(overview) && <Note tone="warn">{t("backup.not_saved")}</Note>}
            <StateCard overview={overview} />
            <KeyringCard overview={overview} />
            <DestinationsCard overview={overview} />
            <PlanCard overview={overview} />
          </div>
        )}
      </Loaded>
    </>
  );
}

// Le dernier ordre de sauvegarde, tel que le plan le garde, et le bouton qui
// en lance un tout de suite.
function StateCard({ overview }: { overview: BackupsOverview }) {
  const t = useT();
  const now = useNow();
  const action = useAction();
  const plan = overview.plan;
  const run = plan?.last_run ?? null;
  const output = run === null ? "" : run.output.map((line) => line.line).join("\n");
  return (
    <Card>
      <CardHeader
        title={t("backup.last_title")}
        aside={
          <Button small icon="archive" disabled={action.busy || !isBackedUp(overview)} onClick={() => action.run(runBackupNow, { topic: "backups" })}>
            {t("backup.run_now")}
          </Button>
        }
      />
      <CardBody gap={10}>
        <Failure error={action.error} />
        {plan !== null && plan.last_error !== "" && <Note tone="danger">{t("backup.last_error", t(plan.last_error))}</Note>}
        {run === null && <p className="secondary">{t("backup.last_none")}</p>}
        {run !== null && (
          <>
            <KeyValue label={t("backup.last_attempt")} value={formatWhen(run.requested_at, now)} />
            <div>
              <Pill tone={stateTones[run.state] ?? "neutral"}>{t(`action.state_${run.state}`)}</Pill>
              {run.reason !== "" && <div className="secondary">{reasonText(t, run.reason)}</div>}
              {output !== "" && <pre className="secondary">{output}</pre>}
            </div>
          </>
        )}
        {plan !== null && <p className="secondary">{t("backup.schedule_daily")}</p>}
      </CardBody>
    </Card>
  );
}

// La paire de clés : l'empreinte de la publique, et le formulaire de la
// phrase qui scelle la privée, demandée deux fois.
function KeyringCard({ overview }: { overview: BackupsOverview }) {
  const t = useT();
  const now = useNow();
  const keyring = overview.keyring;
  const form = useForm({ first: "", second: "" });
  const { values, change, update } = form;
  const action = useAction();
  const [refusal, setRefusal] = useState<string | null>(null);
  const replacing = keyring !== null;
  const save = () => {
    const problem = passphraseProblem(values.first, values.second);
    setRefusal(problem);
    if (problem !== null) {
      return;
    }
    // Remplacer demande confirmation : les anciennes sauvegardes gardent
    // l'ancienne clé, et il faut garder l'ancienne phrase.
    action.run(() => createKeyring(values.first, replacing), {
      ...(replacing && { confirm: t("backup.keyring_replace_confirm") }),
      topic: "backups",
      onDone: () => update({ first: "", second: "" }),
    });
  };
  return (
    <FormCard
      width="fill"
      onSubmit={save}
      error={refusal ?? action.error}
      header={<CardHeader title={t("backup.keyring_title")} />}
      actions={<FormActions submitLabel={replacing ? t("backup.keyring_replace") : t("backup.keyring_create")} submitIcon="shield" busy={action.busy} />}
    >
      {keyring === null ? (
        <p className="secondary">{t("backup.keyring_none")}</p>
      ) : (
        <>
          <KeyValue label={t("backup.fingerprint")} value={keyring.fingerprint} mono />
          <KeyValue label={t("backup.created")} value={formatWhen(keyring.created_at, now)} />
        </>
      )}
      <Field label={t("backup.passphrase_label")} htmlFor="backup-passphrase" help={t("backup.passphrase_help")}>
        <Input id="backup-passphrase" type="password" autoComplete="new-password" maxLength={1024} value={values.first} onChange={(event) => change("first", event.target.value)} required />
      </Field>
      <Field label={t("backup.passphrase_confirm_label")} htmlFor="backup-passphrase-confirm">
        <Input id="backup-passphrase-confirm" type="password" autoComplete="new-password" maxLength={1024} value={values.second} onChange={(event) => change("second", event.target.value)} required />
      </Field>
    </FormCard>
  );
}

// Les machines dont l'agent reçoit les sauvegardes, et le formulaire qui en
// ajoute une.
function DestinationsCard({ overview }: { overview: BackupsOverview }) {
  const t = useT();
  const machines = useMachines();
  const form = useForm({ name: "", machine: "", path: "" });
  const { values, change, update } = form;
  const action = useAction();
  const add = () => {
    action.run(() => createBackupDestination({ name: values.name, machine_id: values.machine, path: values.path }), {
      topic: "backups",
      onDone: () => update({ name: "", path: "" }),
    });
  };
  const remove = (destination: BackupDestination) => {
    action.run(() => deleteBackupDestination(destination.id), { topic: "backups" });
  };
  return (
    <FormCard
      width="fill"
      onSubmit={add}
      error={action.error}
      header={<CardHeader title={t("backup.destination_title")} />}
      actions={<FormActions submitLabel={t("backup.destination_add")} submitIcon="plus" busy={action.busy} canSubmit={values.machine !== ""} />}
    >
      {overview.destinations.length === 0 ? (
        <Empty text={t("backup.destination_none")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("backup.destination_name")}</th>
              <th>{t("backup.destination_machine")}</th>
              <th>{t("backup.destination_path")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {overview.destinations.map((destination) => (
              <tr key={destination.id}>
                <td>{destination.name}</td>
                <td>{destination.machine_name}</td>
                <td className="mono">{destination.path}</td>
                <td>
                  <Pill tone={rightTones[destination.right]}>{t(`backup.right_${destination.right}`)}</Pill>
                  <Button small variant="ghost" icon="x" disabled={action.busy} onClick={() => remove(destination)}>
                    {t("backup.destination_delete")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <Field label={t("backup.destination_name")} htmlFor="backup-destination-name">
        <Input id="backup-destination-name" type="text" autoComplete="off" maxLength={63} value={values.name} onChange={(event) => change("name", event.target.value)} required />
      </Field>
      <Field label={t("backup.destination_machine")} htmlFor="backup-destination-machine">
        <MachineSelect id="backup-destination-machine" machines={machines.data?.machines ?? []} value={values.machine} onChange={(id) => change("machine", id)} noneLabel={t("backup.destination_machine_choose")} />
      </Field>
      <Field label={t("backup.destination_path")} htmlFor="backup-destination-path" help={t("backup.destination_path_help")}>
        <Input id="backup-destination-path" mono type="text" autoComplete="off" value={values.path} onChange={(event) => change("path", event.target.value)} required />
      </Field>
    </FormCard>
  );
}

// Le plan : où part la sauvegarde, et combien en garder. Le rythme est celui
// de la nuit, il ne se règle pas.
function PlanCard({ overview }: { overview: BackupsOverview }) {
  const t = useT();
  const plan = overview.plan;
  const form = useForm({ destination: "", keep: "14" });
  const { values, change, update } = form;
  const action = useAction();
  // Les champs suivent le plan quand il change (enregistré ici, ou ailleurs).
  useEffect(() => {
    if (plan !== null) {
      update({ destination: plan.destination_id, keep: String(plan.keep) });
    }
  }, [plan?.destination_id, plan?.keep]);
  const destination = values.destination === "" ? (overview.destinations[0]?.id ?? "") : values.destination;
  const save = () => {
    action.run(() => saveBackupPlan({ destination_id: destination, keep: Number(values.keep) }), { topic: "backups" });
  };
  return (
    <FormCard
      width="fill"
      onSubmit={save}
      error={action.error}
      header={<CardHeader title={t("backup.plan_title")} />}
      actions={
        <FormActions
          submitLabel={t("backup.plan_save")}
          submitIcon="check"
          busy={action.busy}
          canSubmit={destination !== ""}
          deleteLabel={t("backup.plan_delete")}
          onDelete={plan === null ? undefined : () => action.run(deleteBackupPlan, { topic: "backups" })}
        />
      }
    >
      <Field label={t("backup.plan_destination_label")} htmlFor="backup-plan-destination">
        <Select id="backup-plan-destination" value={destination} onChange={(event) => change("destination", event.target.value)}>
          {overview.destinations.map((candidate) => (
            <option key={candidate.id} value={candidate.id}>
              {candidate.name}
            </option>
          ))}
        </Select>
      </Field>
      <Field label={t("backup.plan_keep_label")} htmlFor="backup-plan-keep" help={t("backup.plan_keep_help")}>
        <Input id="backup-plan-keep" type="number" inputMode="numeric" min={1} max={365} value={values.keep} onChange={(event) => change("keep", event.target.value)} required />
      </Field>
      <p className="secondary">{t("backup.schedule_daily")}</p>
    </FormCard>
  );
}
