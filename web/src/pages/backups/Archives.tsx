import { deleteBackupDestination, useBackups, type BackupDestination, type BackupRight, type Backups } from "../../api/backups";
import { reasonText } from "../../components/ActionsCard";
import { Button } from "../../components/Button";
import { Empty, KeyValue, Note } from "../../components/Card";
import { Disclosure, FoldSection } from "../../components/Disclosure";
import { Failure } from "../../components/Failure";
import { Loaded } from "../../components/Loaded";
import { MissingRightButton } from "../../components/MissingRight";
import { Pill } from "../../components/Pill";
import { SectionHead } from "../../components/SectionHead";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT, type Translate } from "../../i18n/context";
import { counted } from "../../lib/counted";
import { isBackedUp, keyringPath, newDestinationPath, newPlanPath } from "../../lib/backups";
import type { Tone } from "../../lib/tone";
import { formatWhen } from "../../lib/time";
import { PlanCard } from "./PlanCard";

const rightTones: Record<BackupRight, Tone> = { ok: "ok", missing: "danger", unavailable: "warn", unknown: "neutral" };

// Les archives chiffrées de la base d'openCloud : la clé, les destinations et
// les plans. Hors du chemin principal, repliées tant que la base est
// sauvegardée ; ouvertes, en tête de leur pli, quand il manque quelque chose.
// Chaque ajout s'ouvre en fenêtre, à son adresse.
export function ArchivesSection() {
  const t = useT();
  const backups = useBackups();
  return (
    <section id="archives" className="stack" aria-labelledby="backup-archives-title">
      <SectionHead id="backup-archives-title" title={t("backup.archives_title")} text={t("backup.archives_text")} />
      <Loaded resource={backups}>
        {(loaded) => (
          <Disclosure title={t("backup.archives_fold")} aside={archivesAside(t, loaded)} initiallyOpen={!isBackedUp(loaded)}>
            <div className="card-b gap-24">
              {!isBackedUp(loaded) && <Note tone="warn">{t("backup.not_saved")}</Note>}
              <KeyringPart backups={loaded} />
              <DestinationsPart backups={loaded} />
              <PlansPart backups={loaded} />
            </div>
          </Disclosure>
        )}
      </Loaded>
    </section>
  );
}

function archivesAside(t: Translate, backups: Backups): string {
  return isBackedUp(backups) ? counted(t, "backup.archives_ready", backups.plans.length) : t("backup.archives_missing");
}

// La paire de clés : l'empreinte de la publique, et la fenêtre de la phrase
// qui scelle la privée.
function KeyringPart({ backups }: { backups: Backups }) {
  const t = useT();
  const now = useNow();
  const keyring = backups.keyring;
  return (
    <FoldSection title={t("backup.keyring_title")} text={t("backup.keyring_text")}>
      {keyring === null ? (
        <p className="secondary">{t("backup.keyring_none")}</p>
      ) : (
        <>
          <KeyValue label={t("backup.fingerprint")} value={keyring.fingerprint} mono />
          <KeyValue label={t("backup.created")} value={formatWhen(keyring.created_at, now)} />
        </>
      )}
      <div className="cluster">
        <Button small icon="shield" to={keyringPath}>
          {keyring === null ? t("backup.keyring_create") : t("backup.keyring_replace")}
        </Button>
      </div>
    </FoldSection>
  );
}

// Les machines dont l'agent reçoit les archives : une ligne chacune, son droit,
// et le geste qui le donne quand il manque.
function DestinationsPart({ backups }: { backups: Backups }) {
  const t = useT();
  const action = useAction();
  const remove = (destination: BackupDestination) => {
    action.run(() => deleteBackupDestination(destination.id), { topic: "backups" });
  };
  return (
    <FoldSection title={t("backup.destination_title")} text={t("backup.destination_text")}>
      {backups.destinations.length === 0 ? (
        <Empty text={t("backup.destination_none")} />
      ) : (
        <ul className="backup-rows">
          {backups.destinations.map((destination) => (
            <li key={destination.id} className="backup-row">
              <span className="medium">{destination.name}</span>
              <span className="secondary">
                {destination.machine_name} · <span className="mono">{destination.path}</span>
              </span>
              <span className="backup-row-end">
                <Pill small tone={rightTones[destination.right]}>
                  {t(`backup.right_pill_${destination.right}`)}
                </Pill>
                {destination.right === "unavailable" && <span className="secondary">{reasonText(t, destination.right_reason ?? "")}</span>}
                {destination.right === "missing" && <MissingRightButton machineID={destination.machine_id} right="backup.receive" path={destination.path} />}
                <Button small variant="ghost" icon="x" disabled={action.busy} onClick={() => remove(destination)}>
                  {t("backup.destination_delete")}
                </Button>
              </span>
            </li>
          ))}
        </ul>
      )}
      <Failure error={action.error} />
      <div className="cluster">
        <Button small icon="plus" to={newDestinationPath}>
          {t("backup.destination_add")}
        </Button>
      </div>
    </FoldSection>
  );
}

function PlansPart({ backups }: { backups: Backups }) {
  const t = useT();
  return (
    <FoldSection title={t("backup.plans_title")} text={t("backup.plans_text")}>
      {backups.plans.length === 0 && <p className="secondary">{t("backup.plans_none")}</p>}
      {backups.plans.map((plan) => (
        <PlanCard key={plan.subject} plan={plan} overview={backups} />
      ))}
      <div className="cluster">
        <Button small icon="plus" to={newPlanPath}>
          {t("backup.plan_add_title")}
        </Button>
      </div>
    </FoldSection>
  );
}
