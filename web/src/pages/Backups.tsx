import { useState } from "react";
import { Outlet } from "react-router";

import { useBackupOverview, type BackupOverview, type BackupSource } from "../api/backups";
import { ActionButton } from "../components/ActionButton";
import { Button } from "../components/Button";
import { EmptyLine } from "../components/Card";
import { Choice } from "../components/Choice";
import { Loaded } from "../components/Loaded";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { SectionHead } from "../components/SectionHead";
import { useNow } from "../hooks/useNow";
import { useI18n, useT, type Translate } from "../i18n/context";
import { filterSources, isNothingSaved, lastSuccessAt, nextRunAt, sortSources, sourceFilters, sourceStatus, verdictOf, type SourceFilter } from "../lib/backupOverview";
import { counted } from "../lib/counted";
import { newResticBackupPath, newTargetPath } from "../lib/backups";
import { ago, formatRemaining } from "../lib/time";
import { bytesQuantity } from "../lib/units";
import { AccessSection } from "./backups/Access";
import { ArchivesSection } from "./backups/Archives";
import { SourceCard } from "./backups/SourceCard";
import { TargetsSection } from "./backups/Targets";
import { WatchSection } from "./backups/Watch";
import "./Backups.scss";

// Les sauvegardes : ce qui est sauvegardé (et ce qui ne l'est pas), où ça va,
// et comment y accéder. Une carte par VM ou par service, qu'elle soit faite
// par Proxmox de lui-même, par les archives d'openCloud ou par restic. Chaque
// ajout s'ouvre en fenêtre, à son adresse.
export function Backups() {
  const t = useT();
  const overview = useBackupOverview();
  return (
    <>
      <PageHead
        title={t("backup.title")}
        subtitle={t("backup.subtitle")}
        actions={
          <>
            <Button icon="server" to={newTargetPath}>
              {t("backup.make_target")}
            </Button>
            <ActionButton route="POST /api/machines/{id}/restic-backups" icon="plus" to={newResticBackupPath}>
              {t("backup.save_ellipsis")}
            </ActionButton>
          </>
        }
      />
      <Loaded resource={overview}>
        {(loaded) => (
          <div className="stack gap-40">
            <BackupVerdict overview={loaded} />
            <WatchSection />
            <SourcesSection overview={loaded} />
            <TargetsSection overview={loaded} />
            <AccessSection sources={loaded.sources} />
            <ArchivesSection />
          </div>
        )}
      </Loaded>
      {/* La fenêtre de l'adresse : l'assistant, une cible, la clé, une destination, un plan. */}
      <Outlet />
    </>
  );
}

// Le verdict : combien sont à jour, la première source qui attend un geste,
// puis trois chiffres (à jour, libre sur la cible, avant la prochaine).
function BackupVerdict({ overview }: { overview: BackupOverview }) {
  const { t, language } = useI18n();
  const now = useNow(30_000);
  const verdict = verdictOf(overview.sources);
  if (isNothingSaved(overview)) {
    return (
      <section className="card verdict backup-verdict" role="status">
        <Pill tone="neutral">{t("backup.verdict_nothing")}</Pill>
        <div className="backup-verdict-say">
          <b>{t("backup.verdict_nothing_fact")}</b>
          <span>{t("backup.verdict_nothing_context")}</span>
        </div>
      </section>
    );
  }
  const last = lastSuccessAt(overview.sources);
  const next = nextRunAt(overview.sources, now);
  const target = overview.targets.find((candidate) => candidate.kind === "restic" && candidate.disk_total_bytes > 0);
  const free = target === undefined ? null : bytesQuantity(t, language, target.disk_free_bytes);
  return (
    <section className={`card verdict verdict-${verdict.tone} backup-verdict`} role="status">
      <Pill tone={verdict.tone}>{verdictPill(t, verdict.failed, verdict.attention)}</Pill>
      <div className="backup-verdict-say">
        <b>{last === null ? t("backup.verdict_fact_never", verdict.protectedCount, verdict.total) : t("backup.verdict_fact", verdict.protectedCount, verdict.total, ago(t, last, now))}</b>
        <span>{verdict.first === null ? t("backup.verdict_all_good") : firstProblem(t, verdict.first)}</span>
      </div>
      <dl className="backup-verdict-facts">
        <div>
          <dd>
            {verdict.protectedCount}
            <small>{t("backup.verdict_of", verdict.total)}</small>
          </dd>
          <dt>{t("backup.verdict_protected")}</dt>
        </div>
        {target !== undefined && free !== null && (
          <div>
            <dd>
              {free.value}
              <small>{free.unit}</small>
            </dd>
            <dt>{t("backup.verdict_free_on", target.name)}</dt>
          </div>
        )}
        {next !== null && (
          <div>
            <dd>{formatRemaining(t, Date.parse(next) - now)}</dd>
            <dt>{t("backup.verdict_next")}</dt>
          </div>
        )}
      </dl>
    </section>
  );
}

function verdictPill(t: Translate, failed: number, attention: number): string {
  if (failed > 0) {
    return counted(t, "backup.verdict_failed", failed);
  }
  return attention > 0 ? counted(t, "backup.verdict_attention", attention) : t("backup.verdict_ok");
}

// La première source à traiter, en une phrase : son échec, ou qu'elle n'a
// aucune copie.
function firstProblem(t: Translate, source: BackupSource): string {
  const name = source.kind === "database" ? t("backup.source_database") : source.name;
  return t(`backup.verdict_first_${sourceStatus(source)}`, name, source.machine_name);
}

// « Ce qui est sauvegardé » : les cartes, ce qui demande un geste en tête,
// avec un filtre quand la liste s'allonge.
function SourcesSection({ overview }: { overview: BackupOverview }) {
  const t = useT();
  const [filter, setFilter] = useState<SourceFilter>("all");
  const sorted = sortSources(overview.sources);
  const shown = filterSources(sorted, filter);
  return (
    <section className="stack" aria-labelledby="backup-sources-title">
      <SectionHead
        id="backup-sources-title"
        title={t("backup.sources_title")}
        text={t("backup.sources_text")}
        aside={
          <Choice
            label={t("backup.filter_label")}
            name="backup-filter"
            choices={sourceFilters}
            value={filter}
            text={(choice) => t(`backup.filter_${choice}`)}
            count={(choice) => filterSources(sorted, choice).length}
            onChange={setFilter}
          />
        }
      />
      {shown.length === 0 ? (
        <EmptyLine text={t(`backup.filter_empty_${filter}`)} />
      ) : (
        <div className="backup-grid">
          {shown.map((source) => (
            <SourceCard key={source.key} source={source} overview={overview} />
          ))}
        </div>
      )}
    </section>
  );
}
