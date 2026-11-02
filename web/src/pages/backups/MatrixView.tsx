import { useState } from "react";

import type { BackupOverview, BackupProtection, BackupSource } from "../../api/backups";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { Inspector } from "../../components/Inspector";
import { Matrix, type MatrixPick } from "../../components/Matrix";
import { Pill } from "../../components/Pill";
import { useI18n, type Translate } from "../../i18n/context";
import { retentionView, sourceStatus, statusTones } from "../../lib/backupOverview";
import { matrixColumns, matrixGroups, nightsOf, pickedOf } from "../../lib/backupMatrix";
import { ago, formatWhen } from "../../lib/time";
import { formatBytes } from "../../lib/units";
import { useProtectionGestures } from "./gestures";
import { sourceIcons, whatIsMissing } from "./SourceCard";

const legend = [
  { tone: "ok", label: "backup.matrix_legend_ok" },
  { tone: "warn", label: "backup.matrix_legend_warn" },
  { tone: "danger", label: "backup.matrix_legend_danger" },
  { tone: "observed", label: "backup.matrix_legend_observed" },
  { tone: "none", label: "backup.matrix_legend_none" },
] as const;

// La vue « Matrice » de ce qui est sauvegardé : chaque source en ligne, chaque
// endroit où des copies arrivent en colonne. Une case ouvre l'inspecteur à
// droite ; les cartes restent la vue qui montre tout.
export function MatrixView({ overview }: { overview: BackupOverview }) {
  const { t } = useI18n();
  const [pick, setPick] = useState<MatrixPick | null>(null);
  const picked = pickedOf(overview.sources, pick);
  return (
    <div className={picked === null ? "backup-matrix" : "backup-matrix backup-matrix-open"}>
      <Matrix
        caption={t("backup.matrix_caption")}
        rowHeader={t("backup.matrix_source")}
        columns={matrixColumns(t, overview.sources)}
        groups={matrixGroups(t, overview.sources)}
        legend={legend.map((item) => ({ tone: item.tone, label: t(item.label) }))}
        selected={pick}
        onSelect={setPick}
        cellName={(row, column, cell) => t("backup.matrix_cell", row.label, column.label, cell.label)}
      />
      {picked !== null && <PickedInspector overview={overview} source={picked.source} protection={picked.protection} onClose={() => setPick(null)} />}
    </div>
  );
}

function PickedInspector({ overview, source, protection, onClose }: { overview: BackupOverview; source: BackupSource; protection: BackupProtection | null; onClose: () => void }) {
  const { t } = useI18n();
  const title = source.kind === "database" ? t("backup.source_database") : source.name;
  const status = sourceStatus({ protections: protection === null ? [] : [protection], observed_unknown: source.observed_unknown });
  return (
    <Inspector
      icon={sourceIcons[source.kind]}
      title={title}
      subtitle={source.machine_name}
      verdict={
        <Pill tone={statusTones[status]} small>
          {t(`backup.status_${status}`)}
        </Pill>
      }
      onClose={onClose}
      actions={protection === null ? <StartAction t={t} source={source} overview={overview} /> : <ProtectionActions source={source} protection={protection} />}
    >
      {protection !== null && <ProtectionSummary source={source} protection={protection} />}
    </Inspector>
  );
}

// Une source sans copie : le geste qui commence, ou ce qui l'en empêche.
function StartAction({ t, source, overview }: { t: Translate; source: BackupSource; overview: BackupOverview }) {
  const missing = whatIsMissing(t, source, overview);
  return (
    <>
      <p className="secondary">{missing.text}</p>
      {missing.start !== null && (
        <Button variant="primary" icon="plus" to={missing.start}>
          {t("backup.save")}
        </Button>
      )}
      {missing.unfit?.link !== undefined && <Button to={missing.unfit.link.to}>{missing.unfit.link.label}</Button>}
    </>
  );
}

// Les gestes de la protection : ceux du menu de sa carte, en boutons. Chaque
// geste dit son retour arrière avant d'agir.
function ProtectionActions({ source, protection }: { source: BackupSource; protection: BackupProtection }) {
  const { t } = useI18n();
  const gestures = useProtectionGestures(source, protection);
  return (
    <>
      {gestures.menu.map((action) =>
        "to" in action ? (
          <Button key={action.label} to={action.to}>
            {action.label}
          </Button>
        ) : (
          <Button key={action.label} variant={action.danger === true ? "danger" : "secondary"} onClick={action.onSelect}>
            {action.label}
          </Button>
        ),
      )}
      <Failure error={gestures.error} gesture={{ machineID: gestures.refusedHolder }} />
      {gestures.dialogs}
      <p className="secondary">{t(`backup.inspector_back_${protection.origin}`, protection.target_machine_name)}</p>
    </>
  );
}

function ProtectionSummary({ source, protection }: { source: BackupSource; protection: BackupProtection }) {
  const { t, language } = useI18n();
  const now = Date.now();
  const nights = nightsOf(protection);
  const known = nights.filter((tone) => tone !== "none");
  const retention = retentionView(t, protection, source.machine_name);
  return (
    <>
      {known.length > 0 && (
        <div>
          <span className="backup-label">{t("backup.inspector_nights")}</span>
          <div className="backup-strip" role="img" aria-label={t("backup.inspector_nights_label", known.filter((tone) => tone === "ok").length, known.length)}>
            {nights.map((tone, index) => (
              <i key={index} className={`backup-strip-${tone}`} />
            ))}
          </div>
        </div>
      )}
      <dl className="backup-facts">
        <div className="backup-item">
          <dt>{t("backup.fact_last")}</dt>
          <dd>{protection.last_at === null ? t("backup.last_never") : ago(t, protection.last_at, now)}</dd>
          {protection.last_at !== null && <dd className="backup-item-sub">{formatWhen(protection.last_at, now)}</dd>}
        </div>
        <div className="backup-item">
          <dt>{t("backup.fact_next")}</dt>
          <dd>{protection.next_at === null ? "—" : formatWhen(protection.next_at, now)}</dd>
        </div>
        {protection.size_bytes !== null && (
          <div className="backup-item">
            <dt>{t("backup.fact_size")}</dt>
            <dd className="mono">{formatBytes(t, language, protection.size_bytes)}</dd>
          </div>
        )}
        {protection.copies !== null && (
          <div className="backup-item">
            <dt>{t("backup.fact_copies")}</dt>
            <dd className="mono">{protection.copies}</dd>
          </div>
        )}
        <div className="backup-item backup-item-wide">
          <dt>{t("backup.fact_retention")}</dt>
          <dd>{retention.value}</dd>
          {retention.note !== null && <dd className="backup-item-sub">{retention.note}</dd>}
        </div>
      </dl>
    </>
  );
}
