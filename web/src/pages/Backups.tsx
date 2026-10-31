import { useState } from "react";

import {
  createBackupDestination,
  createKeyring,
  deleteBackupDestination,
  useBackups,
  type BackupDestination,
  type BackupPlan,
  type BackupRight,
  type Backups as BackupsOverview,
} from "../api/backups";
import { useMachines } from "../api/machines";
import { reasonText } from "../components/ActionsCard";
import { Button } from "../components/Button";
import { CardHeader, Empty, EmptyCard, KeyValue, Note } from "../components/Card";
import { Field, Input } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { Loaded } from "../components/Loaded";
import { MachineSelect } from "../components/MachineSelect";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { SectionHead } from "../components/SectionHead";
import { Table } from "../components/Table";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { isBackedUp, passphraseProblem } from "../lib/backups";
import type { Tone } from "../lib/tone";
import { formatWhen } from "../lib/time";
import { PlanCard } from "./backups/PlanCard";
import { Inventory } from "./backups/Inventory";
import { PlanForm } from "./backups/PlanForm";

const rightTones: Record<BackupRight, Tone> = { ok: "ok", missing: "danger", unavailable: "warn", unknown: "neutral" };

// Les sauvegardes : la clé et la destination qui les rendent possibles, puis
// les plans, chacun avec sa source et sa destination. La phrase de passe
// n'est lue qu'ici, le temps de créer la clé.
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
            <KeyringCard overview={overview} />
            <DestinationsCard overview={overview} />
            <PlansSection overview={overview} />
            <Inventory />
          </div>
        )}
      </Loaded>
    </>
  );
}

// Les plans, un bloc chacun, et le formulaire qui en ajoute un ou modifie
// celui qu'on a choisi.
function PlansSection({ overview }: { overview: BackupsOverview }) {
  const t = useT();
  const [editing, setEditing] = useState<BackupPlan | null>(null);
  return (
    <>
      <SectionHead id="backup-plans-title" title={t("backup.plans_title")} text={t("backup.plans_text")} />
      {overview.plans.length === 0 && <EmptyCard text={t("backup.plans_none")} />}
      {overview.plans.map((plan) => (
        <PlanCard key={plan.subject} plan={plan} overview={overview} onEdit={setEditing} />
      ))}
      <PlanForm key={editing?.subject ?? ""} overview={overview} editing={editing} onDone={() => setEditing(null)} />
    </>
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
                  {destination.right === "unavailable" && <div className="secondary">{reasonText(t, destination.right_reason ?? "")}</div>}
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
