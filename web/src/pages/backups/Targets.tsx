import { useState } from "react";

import { removeResticTarget, type BackupMachine, type BackupOverview, type BackupSource, type BackupTarget } from "../../api/backups";
import { Button } from "../../components/Button";
import { ConfirmDialog } from "../../components/ConfirmModal";
import { CardGrid, EntityCard } from "../../components/EntityCard";
import { Failure } from "../../components/Failure";
import { Icon } from "../../components/Icon";
import { MoreMenu } from "../../components/MoreMenu";
import { Pill } from "../../components/Pill";
import { SectionHead } from "../../components/SectionHead";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useI18n, useT, type Translate } from "../../i18n/context";
import { diskShare, hostPort, isRestServerMissing, targetFloorText } from "../../lib/backupOverview";
import { newTargetPath } from "../../lib/backups";
import type { Tone } from "../../lib/tone";
import { ago } from "../../lib/time";
import { bytesQuantity, formatBytes, percentOf } from "../../lib/units";
import { InstallBlock } from "./InstallBlock";
import { sourceIcons } from "./SourceCard";

const resticTones: Record<string, Tone> = { running: "ok", stopped: "danger", error: "danger", pending: "neutral" };
const rightTones: Record<string, Tone> = { ok: "ok", missing: "danger", unavailable: "warn", unknown: "neutral" };

// « Où ça va » : les cibles restic d'openCloud en grand (leur disque, leur
// rest-server, ce qu'elles gardent), puis les stockages de Proxmox et les
// destinations des archives, plus petits.
export function TargetsSection({ overview }: { overview: BackupOverview }) {
  const t = useT();
  const restic = overview.targets.filter((target) => target.kind === "restic");
  const others = overview.targets.filter((target) => target.kind !== "restic");
  return (
    <section className="stack" aria-labelledby="backup-targets-title">
      <SectionHead id="backup-targets-title" title={t("backup.targets_title")} text={t("backup.targets_text")} />
      <div className="backup-targets">
        {restic.map((target) => (
          <ResticTargetCard key={target.key} target={target} sources={overview.sources} machine={overview.machines.find((candidate) => candidate.machine_id === target.machine_id)} />
        ))}
        <AddTargetCard first={restic.length === 0} />
      </div>
      {others.length > 0 && (
        <CardGrid>
          {others.map((target) => (
            <OtherTargetCard key={target.key} target={target} />
          ))}
        </CardGrid>
      )}
    </section>
  );
}

// Une cible restic : son disque, son rest-server et le plancher qu'elle tient ;
// rest-server manquant, les commandes pour l'installer sur la machine nommée.
function ResticTargetCard({ target, sources, machine }: { target: BackupTarget; sources: readonly BackupSource[]; machine: BackupMachine | undefined }) {
  const t = useT();
  const action = useAction();
  const [removing, setRemoving] = useState(false);
  const inUse = target.holds.length > 0;
  return (
    <article className="card backup-target" aria-labelledby={`backup-target-${target.machine_id}`}>
      <header className="backup-target-head">
        <span className="backup-badge" aria-hidden="true">
          <Icon name="server" />
        </span>
        <h3 id={`backup-target-${target.machine_id}`}>{target.name}</h3>
        <TargetState target={target} />
        <MoreMenu
          label={t("entity.more", target.name)}
          actions={[
            {
              label: t("backup.target_remove"),
              danger: true,
              disabled: inUse,
              ...(inUse ? { hint: t("backup.target_remove_in_use") } : {}),
              onSelect: () => setRemoving(true),
            },
          ]}
        />
      </header>
      <div className="backup-target-body">
        <TargetDisk target={target} />
        <div>
          <div className="backup-label">{t("backup.target_server")}</div>
          <dl className="backup-kv">
            <dt>{t("backup.target_address")}</dt>
            <dd className="mono">{target.host === "" ? "—" : hostPort(target.host, target.port)}</dd>
            <dt>{t("backup.target_dir")}</dt>
            <dd className="mono">{target.dir}</dd>
            <dt>{t("backup.target_mode")}</dt>
            <dd>{t("backup.target_append_only")}</dd>
            <dt>{t("backup.target_purge")}</dt>
            <dd>{t("backup.target_purge_by", target.name)}</dd>
            <dt>{t("backup.target_version")}</dt>
            <dd className="mono">{target.version === "" ? "—" : target.version}</dd>
            <dt>{t("backup.target_certificate")}</dt>
            <dd className="mono" title={target.fingerprint}>
              {shortFingerprint(target.fingerprint)}
            </dd>
          </dl>
          <p className="backup-floor">{targetFloorText(t, target)}</p>
        </div>
      </div>
      {isRestServerMissing(target) && machine !== undefined && <InstallBlock software="rest-server" machine={machine} />}
      <TargetHolds target={target} sources={sources} />
      <Failure error={action.error} />
      {removing && (
        <ConfirmDialog
          title={t("backup.target_remove")}
          question={t("backup.target_remove_confirm", target.name)}
          confirmLabel={t("backup.target_remove")}
          onConfirm={() => action.run(() => removeResticTarget(target.machine_id), { topic: "backups" })}
          onClose={() => setRemoving(false)}
        />
      )}
    </article>
  );
}

// L'état de rest-server ; une machine qui ne signale plus est neutre et garde
// son dernier état connu dessous.
function TargetState({ target }: { target: BackupTarget }) {
  const t = useT();
  const now = useNow(30_000);
  if (!target.online) {
    return (
      <span className="backup-target-state">
        <Pill small tone="neutral">
          {t("backup.target_silent")}
        </Pill>
        {target.reported_at !== null && <span className="secondary">{t("backup.target_last_state", t(`backup.target_state_${target.state}`), ago(t, target.reported_at, now))}</span>}
      </span>
    );
  }
  if (isRestServerMissing(target)) {
    return (
      <span className="backup-target-state">
        <Pill small tone="warn">
          {t("backup.target_rest_server_missing", target.name)}
        </Pill>
      </span>
    );
  }
  return (
    <span className="backup-target-state">
      <Pill small tone={resticTones[target.state] ?? "neutral"}>
        {t(`backup.target_state_${target.state}`)}
      </Pill>
    </span>
  );
}

// Le disque des dépôts : le libre en grand, la jauge en trois parts.
function TargetDisk({ target }: { target: BackupTarget }) {
  const { t, language } = useI18n();
  const now = useNow(30_000);
  const share = diskShare(target);
  if (share === null) {
    return (
      <div>
        <div className="backup-label">{t("backup.target_disk")}</div>
        <p className="secondary">{t("backup.target_disk_unknown")}</p>
      </div>
    );
  }
  const free = bytesQuantity(t, language, share.free);
  const label = t("backup.target_disk_bar", formatBytes(t, language, share.backup), formatBytes(t, language, share.other), formatBytes(t, language, share.free));
  return (
    <div>
      <div className="backup-label">{t("backup.target_disk")}</div>
      <div className="backup-big">
        {free.value}
        <small>{t("backup.target_free_of", free.unit, formatBytes(t, language, target.disk_total_bytes))}</small>
      </div>
      <svg className={target.online ? "backup-bar" : "backup-bar backup-bar-stale"} width="100%" height="8" role="img" aria-label={label}>
        <rect className="backup-bar-track" width="100%" height="8" rx="4" />
        <rect className="backup-bar-other" x={`${share.backupPercent}%`} width={`${share.otherPercent}%`} height="8" />
        <rect className="backup-bar-backup" width={`${share.backupPercent}%`} height="8" rx="4" />
      </svg>
      <div className="backup-legend">
        <span>
          <i className="backup-legend-backup" />
          {t("backup.target_legend_backups")} <b className="mono">{formatBytes(t, language, share.backup)}</b>
        </span>
        <span>
          <i className="backup-legend-other" />
          {t("backup.target_legend_other")} <b className="mono">{formatBytes(t, language, share.other)}</b>
        </span>
        <span>
          <i className="backup-legend-free" />
          {t("backup.target_legend_free")} <b className="mono">{formatBytes(t, language, share.free)}</b>
        </span>
      </div>
      {target.reported_at !== null && <p className="secondary">{t("backup.target_reported", ago(t, target.reported_at, now))}</p>}
    </div>
  );
}

// Ce que la cible garde, une pastille par source.
function TargetHolds({ target, sources }: { target: BackupTarget; sources: readonly BackupSource[] }) {
  const t = useT();
  const held = target.holds.map((key) => sources.find((source) => source.key === key)).filter((source): source is BackupSource => source !== undefined);
  return (
    <div className="backup-holds">
      <div className="backup-label">{t("backup.target_holds")}</div>
      {held.length === 0 ? (
        <p className="secondary">{t("backup.target_holds_none")}</p>
      ) : (
        <ul className="backup-chips">
          {held.map((source) => (
            <li key={source.key} className="backup-chip">
              <Icon name={sourceIcons[source.kind]} />
              <b>{source.kind === "database" ? t("backup.source_database") : source.name}</b>
              <span className="secondary">{source.machine_name}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// L'empreinte du certificat, abrégée ; entière au survol.
function shortFingerprint(fingerprint: string): string {
  if (fingerprint.length <= 12) {
    return fingerprint === "" ? "—" : fingerprint;
  }
  return `${fingerprint.slice(0, 4)}…${fingerprint.slice(-4)}`;
}

function AddTargetCard({ first }: { first: boolean }) {
  const t = useT();
  return (
    <div className="card backup-add">
      <span className="backup-badge" aria-hidden="true">
        <Icon name="plus" />
      </span>
      <b>{first ? t("backup.target_add_first") : t("backup.target_add_other")}</b>
      <p className="secondary">{first ? t("backup.target_add_first_text") : t("backup.target_add_other_text")}</p>
      <div>
        <Button icon="server" to={newTargetPath}>
          {t("backup.make_target")}
        </Button>
      </div>
    </div>
  );
}

// Un stockage de Proxmox ou une destination des archives : une carte d'objet.
function OtherTargetCard({ target }: { target: BackupTarget }) {
  const { t, language } = useI18n();
  const isStorage = target.kind === "storage";
  const meters =
    target.disk_total_bytes > 0
      ? [
          {
            label: t("backup.target_used"),
            value: `${formatBytes(t, language, target.disk_used_bytes)} / ${formatBytes(t, language, target.disk_total_bytes)}`,
            percent: percentOf(target.disk_used_bytes, target.disk_total_bytes),
          },
        ]
      : [];
  return (
    <EntityCard
      icon={isStorage ? "disk" : "archive"}
      name={target.name}
      kind={t(`backup.target_kind_${target.kind}`, target.machine_name)}
      state={<OtherTargetState t={t} target={target} />}
      facts={[
        { label: t("backup.target_place"), value: isStorage ? `${target.node} · ${target.type}` : target.dir },
        { label: t("backup.target_holds"), value: target.holds.length, mono: true },
      ]}
      meters={meters}
    />
  );
}

function OtherTargetState({ t, target }: { t: Translate; target: BackupTarget }) {
  if (target.kind === "storage") {
    return (
      <Pill small tone={target.state === "active" ? "ok" : "warn"}>
        {t(`backup.storage_${target.state}`)}
      </Pill>
    );
  }
  return (
    <Pill small tone={rightTones[target.state] ?? "neutral"}>
      {t(`backup.right_pill_${target.state}`)}
    </Pill>
  );
}
