import { useBackupInventory, type BackupInventory, type BackupSource, type BackupTarget } from "../../api/backups";
import { reasonText } from "../../components/ActionsCard";
import { Empty } from "../../components/Card";
import { Loaded } from "../../components/Loaded";
import { Pill } from "../../components/Pill";
import { SectionHead } from "../../components/SectionHead";
import { Table } from "../../components/Table";
import { useNow } from "../../hooks/useNow";
import { useI18n } from "../../i18n/context";
import { failedAttemptOf, lastBackupOf, protectionOf, runReasonText } from "../../lib/backups";
import type { Tone } from "../../lib/tone";
import { formatWhen } from "../../lib/time";
import { formatBytes } from "../../lib/units";

const rightTones: Record<string, Tone> = { ok: "ok", missing: "danger", unavailable: "warn", unknown: "neutral" };

// L'inventaire : ce qui existe à sauvegarder (les sources que rien ne
// sauvegarde en tête), puis les cibles où cela peut aller. Lecture seule : le
// serveur le calcule de ce qu'il a déjà, et aucun geste n'en part.
export function Inventory() {
  const { t } = useI18n();
  const inventory = useBackupInventory();
  return (
    <>
      <SectionHead id="backup-inventory-title" title={t("backup.inventory_title")} text={t("backup.inventory_text")} />
      <Loaded resource={inventory}>
        {(loaded) => (
          <div className="stack">
            <SourcesTable inventory={loaded} />
            <TargetsTable inventory={loaded} />
          </div>
        )}
      </Loaded>
    </>
  );
}

function SourcesTable({ inventory }: { inventory: BackupInventory }) {
  const { t } = useI18n();
  if (inventory.sources.length === 0) {
    return <Empty text={t("backup.inventory_none")} />;
  }
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("backup.inventory_source")}</th>
          <th>{t("backup.inventory_kind")}</th>
          <th>{t("backup.inventory_machine")}</th>
          <th>{t("backup.inventory_covered_by")}</th>
          <th>{t("backup.inventory_last")}</th>
          <th>{t("backup.inventory_size")}</th>
          <th>{t("backup.inventory_target")}</th>
        </tr>
      </thead>
      <tbody>
        {inventory.sources.map((source) => (
          <SourceRow key={source.key} source={source} />
        ))}
      </tbody>
    </Table>
  );
}

function SourceRow({ source }: { source: BackupSource }) {
  const { t, language } = useI18n();
  const now = useNow();
  const last = lastBackupOf(source);
  const failed = failedAttemptOf(source);
  return (
    <tr>
      <td>
        {source.name}
        {source.id !== "" && <span className="secondary mono"> {source.id}</span>}
        {source.kind === "docker_volume" && (
          <div className="secondary">{source.used_by.length === 0 ? t("backup.inventory_unused") : t("backup.inventory_used_by", source.used_by.join(", "))}</div>
        )}
      </td>
      <td>{t(`backup.inventory_kind_${source.kind}`)}</td>
      <td>{source.machine_name}</td>
      <td>
        <Protection source={source} />
      </td>
      <td>
        {last === null ? <span className="secondary">{t("backup.inventory_never")}</span> : formatWhen(last.at, now)}
        {last !== null && <div className="secondary">{t(last.origin === "plan" ? "backup.inventory_origin_plan" : "backup.inventory_origin_observed")}</div>}
        {source.observed !== null && <div className="secondary">{t("backup.inventory_observed_detail", source.observed.count, formatBytes(t, language, source.observed.total_bytes))}</div>}
        {failed !== null && (
          <div className="secondary">
            {failed.reason === ""
              ? t("backup.inventory_last_attempt_plain", t(`backup.run_result_${failed.result}`))
              : t("backup.inventory_last_attempt", t(`backup.run_result_${failed.result}`), runReasonText(t, failed.reason, (reason) => reasonText(t, reason)))}
          </div>
        )}
      </td>
      <td className="mono">{last === null || last.bytes === null ? "" : formatBytes(t, language, last.bytes)}</td>
      <td>{last?.target ?? ""}</td>
    </tr>
  );
}

// Les plans qui couvrent la source ; sinon la pastille qui dit pourquoi on la
// montre en tête : rien ne la sauvegarde, ou Proxmox seul.
function Protection({ source }: { source: BackupSource }) {
  const { t } = useI18n();
  const protection = protectionOf(source);
  if (protection === "none") {
    return <Pill tone="danger">{t("backup.inventory_protection_none")}</Pill>;
  }
  if (protection === "observed") {
    return <Pill tone="warn">{t("backup.inventory_protection_observed")}</Pill>;
  }
  return (
    <>
      {source.covered_by.map((coverage) => (
        <div key={coverage.subject}>{coverage.plan_name === "" ? coverage.subject : coverage.plan_name}</div>
      ))}
    </>
  );
}

// Les cibles. Une destination du serveur et un stockage Proxmox peuvent être
// le même endroit physique : la page les garde séparés et le dit.
function TargetsTable({ inventory }: { inventory: BackupInventory }) {
  const { t } = useI18n();
  return (
    <>
      <SectionHead id="backup-targets-title" title={t("backup.inventory_targets_title")} text={t("backup.inventory_targets_text")} />
      {inventory.targets.length === 0 ? (
        <Empty text={t("backup.inventory_targets_none")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("backup.inventory_target_name")}</th>
              <th>{t("backup.inventory_target_kind")}</th>
              <th>{t("backup.inventory_machine")}</th>
              <th>{t("backup.inventory_target_place")}</th>
              <th>{t("backup.inventory_target_state")}</th>
            </tr>
          </thead>
          <tbody>
            {inventory.targets.map((target) => (
              <TargetRow key={target.key} target={target} />
            ))}
          </tbody>
        </Table>
      )}
      <p className="secondary">{t("backup.inventory_targets_note")}</p>
    </>
  );
}

function TargetRow({ target }: { target: BackupTarget }) {
  const { t, language } = useI18n();
  const isDestination = target.kind === "destination";
  return (
    <tr>
      <td>{target.name}</td>
      <td>{t(`backup.inventory_target_kind_${target.kind}`)}</td>
      <td>{target.machine_name}</td>
      <td className="mono">{isDestination ? target.path : t("backup.inventory_target_storage_place", target.node, target.type)}</td>
      <td>
        {isDestination && target.right !== undefined && <Pill tone={rightTones[target.right] ?? "neutral"}>{t(`backup.right_${target.right}`)}</Pill>}
        {!isDestination && !target.active && <Pill tone="warn">{t("backup.inventory_target_inactive")}</Pill>}
        {!isDestination && target.total_bytes > 0 && (
          <div className="secondary">{t("backup.inventory_target_usage", formatBytes(t, language, target.used_bytes), formatBytes(t, language, target.total_bytes))}</div>
        )}
        {isDestination && target.plans.length > 0 && <div className="secondary">{t("backup.inventory_target_plans", target.plans.join(", "))}</div>}
      </td>
    </tr>
  );
}
