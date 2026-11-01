import { Outlet } from "react-router";

import { deleteBackupDestination, useBackups, type BackupDestination, type BackupRight, type Backups as BackupsOverview } from "../api/backups";
import { reasonText } from "../components/ActionsCard";
import { ActionButton } from "../components/ActionButton";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, EmptyCard, KeyValue, Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Loaded } from "../components/Loaded";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { SectionHead } from "../components/SectionHead";
import { Table } from "../components/Table";
import { useAction } from "../hooks/useAction";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { isBackedUp, keyringPath, newDestinationPath, newPlanPath } from "../lib/backups";
import type { Tone } from "../lib/tone";
import { formatWhen } from "../lib/time";
import { PlanCard } from "./backups/PlanCard";
import { Inventory } from "./backups/Inventory";

const rightTones: Record<BackupRight, Tone> = { ok: "ok", missing: "danger", unavailable: "warn", unknown: "neutral" };

// Les sauvegardes : la clé et la destination qui les rendent possibles, puis
// les plans, chacun avec sa source et sa destination. Chaque ajout s'ouvre en
// fenêtre, à son adresse ; la phrase de passe n'est lue que dans celle de la
// clé.
export function Backups() {
  const t = useT();
  const backups = useBackups();
  return (
    <>
      <PageHead
        title={t("backup.title")}
        subtitle={t("backup.subtitle")}
        actions={
          <ActionButton route="PUT /api/backups/plans/{subject}" icon="plus" to={newPlanPath}>
            {t("backup.plan_add_title")}
          </ActionButton>
        }
      />
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
      {/* La fenêtre de l'adresse : la clé, une destination ou un plan. */}
      <Outlet />
    </>
  );
}

// Les plans, un bloc chacun ; l'ajout est le bouton de l'en-tête.
function PlansSection({ overview }: { overview: BackupsOverview }) {
  const t = useT();
  return (
    <>
      <SectionHead id="backup-plans-title" title={t("backup.plans_title")} text={t("backup.plans_text")} />
      {overview.plans.length === 0 && <EmptyCard text={t("backup.plans_none")} />}
      {overview.plans.map((plan) => (
        <PlanCard key={plan.subject} plan={plan} overview={overview} />
      ))}
    </>
  );
}

// La paire de clés : l'empreinte de la publique, et le bouton qui ouvre la
// fenêtre de la phrase qui scelle la privée.
function KeyringCard({ overview }: { overview: BackupsOverview }) {
  const t = useT();
  const now = useNow();
  const keyring = overview.keyring;
  return (
    <Card>
      <CardHeader title={t("backup.keyring_title")} />
      <CardBody gap={10}>
        {keyring === null ? (
          <p className="secondary">{t("backup.keyring_none")}</p>
        ) : (
          <>
            <KeyValue label={t("backup.fingerprint")} value={keyring.fingerprint} mono />
            <KeyValue label={t("backup.created")} value={formatWhen(keyring.created_at, now)} />
          </>
        )}
        <div className="cluster">
          <Button icon="shield" to={keyringPath}>
            {keyring === null ? t("backup.keyring_create") : t("backup.keyring_replace")}
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}

// Les machines dont l'agent reçoit les sauvegardes, et le bouton qui en
// ajoute une.
function DestinationsCard({ overview }: { overview: BackupsOverview }) {
  const t = useT();
  const action = useAction();
  const remove = (destination: BackupDestination) => {
    action.run(() => deleteBackupDestination(destination.id), { topic: "backups" });
  };
  return (
    <Card>
      <CardHeader title={t("backup.destination_title")} />
      <CardBody gap={10}>
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
        <Failure error={action.error} />
        <div className="cluster">
          <Button icon="plus" to={newDestinationPath}>
            {t("backup.destination_add")}
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}
