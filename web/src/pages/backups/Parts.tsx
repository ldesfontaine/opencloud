import type { BackupPartKind, BackupPartResult, BackupSourcePart } from "../../api/backups";
import { Icon, type IconName } from "../../components/Icon";
import { reasonText } from "../../components/ActionsCard";
import { useI18n, type Translate } from "../../i18n/context";
import { formatBytes } from "../../lib/units";

const partIcons: Record<BackupPartKind, IconName> = { folder: "folder", volume: "disk", database: "database" };
const markIcons = { ok: "check", failed: "x", skipped: "minus", todo: "clock" } as const;

// Ce que contient une sauvegarde de service : son dossier, ses volumes, ses
// bases. Sans résultat, c'est ce qui partira ; avec, ce que chaque partie a
// donné au dernier passage (une partie qui échoue n'arrête pas les autres).
export function PartsBlock({ parts, results }: { parts: readonly BackupSourcePart[]; results: readonly BackupPartResult[] }) {
  const { t, language } = useI18n();
  if (parts.length === 0) {
    return null;
  }
  const title = results.length === 0 ? t("backup.parts_will") : t("backup.parts_has");
  return (
    <div className="backup-parts">
      <h4 className="backup-parts-title">{title}</h4>
      <ul>
        {parts.map((part) => {
          const result = results.find((candidate) => candidate.kind === part.kind && candidate.name === part.name);
          const state = result?.state ?? "todo";
          return (
            <li key={`${part.kind}:${part.name}`} className="backup-part">
              <span className={`backup-part-mark backup-part-${state}`} title={t(`backup.part_${state}`)}>
                <Icon name={markIcons[state]} />
              </span>
              <Icon name={partIcons[part.kind]} />
              <span className="backup-part-name">
                <b>{partName(t, part)}</b>
                <span className="secondary">{partDetail(t, part)}</span>
                {result !== undefined && result.reason !== "" && <span className="backup-item-warn">{reasonText(t, result.reason)}</span>}
              </span>
              {result !== undefined && result.state === "ok" && <span className="mono secondary">+{formatBytes(t, language, result.bytes_added)}</span>}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function partName(t: Translate, part: BackupSourcePart): string {
  return part.kind === "folder" ? t("backup.part_folder") : part.name;
}

function partDetail(t: Translate, part: BackupSourcePart): string {
  if (part.kind === "volume") {
    return t("backup.part_volume");
  }
  return part.kind === "database" ? t("backup.part_database", engineName(t, part.engine)) : "";
}

function engineName(t: Translate, engine: string): string {
  return engine === "" ? "" : t(`backup.part_engine_${engine}`);
}
