import { useState, type ReactNode } from "react";

import { deleteResticBackup, runResticBackup, verifyResticBackup, type BackupMachine, type BackupOverview, type BackupProtection, type BackupSource } from "../../api/backups";
import { errorKey } from "../../api/client";
import { reasonText } from "../../components/ActionsCard";
import { Button } from "../../components/Button";
import { ConfirmDialog } from "../../components/ConfirmModal";
import { TokenBox } from "../../components/Copy";
import { Failure } from "../../components/Failure";
import { Icon, type IconName } from "../../components/Icon";
import { MissingRightButton } from "../../components/MissingRight";
import { MoreMenu, type MenuAction } from "../../components/MoreMenu";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useI18n, useT, type Translate } from "../../i18n/context";
import { directHint, forgetView, pathReasonText, protectionReasonText, retentionView, scopeOf, sourceStatus, sourceSubtitle, sourceUnfit, statusTones, verifyBlockedBy, verifyView, type Unfitness } from "../../lib/backupOverview";
import { editPlanPath, editResticPath, firewallIdOf, newFirewallPlanPath, newPlanPath, newResticBackupPath, newTargetPath } from "../../lib/backups";
import { missingSoftware } from "../../lib/resticInstall";
import { holderOfRefusal, rightOfRefusal } from "../../lib/rights";
import { ago, formatRemaining, formatWhen } from "../../lib/time";
import { formatBytes } from "../../lib/units";
import { InstallBlock } from "./InstallBlock";
import { PartsBlock } from "./Parts";

// L'icône d'une source, d'après ce qu'elle est.
export const sourceIcons: Record<BackupSource["kind"], IconName> = {
  database: "database",
  vm: "monitor",
  lxc: "monitor",
  service: "box",
  docker_volume: "disk",
  firewall: "firewall",
};

// Une carte par source : ce qu'elle est, puis chaque façon dont elle est
// sauvegardée (le trajet, les dates, la taille, la rétention, le chemin). Une
// source que rien ne protège dit ce qu'il manque et porte « Sauvegarder ».
export function SourceCard({ source, overview }: { source: BackupSource; overview: BackupOverview }) {
  const t = useT();
  const status = sourceStatus(source);
  const tint = status === "failed" ? " backup-source-danger" : status === "none" ? " backup-source-warn" : "";
  const titleID = `backup-source-${source.key.replace(/[^a-zA-Z0-9_-]/g, "-")}`;
  return (
    <article className={`card backup-source${tint}`} aria-labelledby={titleID}>
      <header className="backup-source-head">
        <span className="backup-badge" aria-hidden="true">
          <Icon name={sourceIcons[source.kind]} />
        </span>
        <div className="backup-source-title">
          <h3 id={titleID}>{source.kind === "database" ? t("backup.source_database") : source.name}</h3>
          <span className="secondary">{sourceSubtitle(t, source)}</span>
        </div>
        <Pill small tone={statusTones[status]}>
          {t(`backup.status_${status}`)}
        </Pill>
      </header>
      {source.protections.length === 0 ? (
        <Unprotected source={source} overview={overview} />
      ) : (
        source.protections.map((protection) => <ProtectionBlock key={`${protection.origin}:${protection.id}`} source={source} protection={protection} machines={overview.machines} />)
      )}
    </article>
  );
}

// Le trajet : d'où part la copie, comment, et où elle arrive.
function Flow({ from, fromIcon, fromSub, label, to, toSub, tone }: { from: string; fromIcon: IconName; fromSub: string; label: string; to: string; toSub: string; tone: "muted" | "accent" | "warn" | "danger" | "none" }) {
  const t = useT();
  return (
    <div className="backup-flow" role="img" aria-label={t("backup.flow_label", from, label, to)}>
      <div className="backup-flow-node">
        <b>
          <Icon name={fromIcon} />
          <span className="backup-flow-name" title={from}>
            {from}
          </span>
        </b>
        <span className="backup-flow-sub">{fromSub}</span>
      </div>
      <div className={`backup-flow-wire backup-flow-${tone}`}>
        <em>{label}</em>
      </div>
      <div className="backup-flow-node backup-flow-to">
        <b>
          {to !== "—" && <Icon name="server" />}
          <span className="backup-flow-name" title={to}>
            {to}
          </span>
        </b>
        <span className="backup-flow-sub">{toSub}</span>
      </div>
    </div>
  );
}

// Un fait : son étiquette, sa valeur, une ligne discrète dessous, et parfois
// une seconde (la dernière purge).
function Fact({ label, value, sub, more, mono = false, wide = false }: { label: string; value: ReactNode; sub?: string | null; more?: ReactNode; mono?: boolean; wide?: boolean }) {
  return (
    <div className={wide ? "backup-item backup-item-wide" : "backup-item"}>
      <dt>{label}</dt>
      <dd className={mono ? "mono" : undefined}>{value}</dd>
      {sub !== undefined && sub !== null && sub !== "" && <dd className="backup-item-sub">{sub}</dd>}
      {more}
    </div>
  );
}

// Une source que rien ne sauvegarde : le trajet vide, ce qu'il manque, et le
// geste qui commence.
function Unprotected({ source, overview }: { source: BackupSource; overview: BackupOverview }) {
  const t = useT();
  const missing = whatIsMissing(t, source, overview);
  const unknown = source.observed_unknown;
  return (
    <div className="backup-protection">
      <Flow from={source.name} fromIcon={sourceIcons[source.kind]} fromSub={source.machine_name} label={t("backup.flow_nothing")} to="—" toSub={t("backup.flow_no_target")} tone="none" />
      <PartsBlock parts={source.parts} results={[]} />
      <dl className="backup-facts">
        <Fact label={t("backup.fact_last")} value={unknown ? t("backup.last_unknown") : t("backup.last_never")} sub={unknown ? t("backup.last_unknown_sub") : t("backup.last_never_sub")} />
        <Fact label={t("backup.fact_missing")} value={missing.text} sub={missing.sub} wide />
      </dl>
      {missing.unfit?.install !== undefined && <InstallBlock software={missing.unfit.install.software} machine={missing.unfit.install.machine} />}
      <div className="backup-foot">
        {missing.unfit?.right !== undefined && <MissingRightButton machineID={missing.unfit.right.machineID} right={missing.unfit.right.name} path={missing.unfit.right.path} short />}
        {missing.unfit?.right === undefined && missing.unfit?.link !== undefined && (
          <Button small to={missing.unfit.link.to}>
            {missing.unfit.link.label}
          </Button>
        )}
        {missing.start !== null && (
          <Button small variant="primary" icon="plus" to={missing.start}>
            {t("backup.save")}
          </Button>
        )}
        {missing.foot !== null && <span className="secondary">{missing.foot}</span>}
      </div>
    </div>
  );
}

interface Missing {
  text: string;
  sub: string | null;
  unfit: Unfitness | null;
  // L'adresse de l'assistant qui commence ; null quand rien ne peut partir.
  start: string | null;
  foot: string | null;
}

// Ce qu'il manque pour que la source parte : la base passe par un plan
// d'archives ; un volume Docker n'est pas couvert ; sinon un droit, une
// machine en ligne, ou une cible prête.
function whatIsMissing(t: Translate, source: BackupSource, overview: BackupOverview): Missing {
  if (source.kind === "database") {
    return { text: t("backup.missing_plan"), sub: t("backup.missing_plan_sub"), unfit: null, start: newPlanPath, foot: null };
  }
  if (source.kind === "firewall") {
    return { text: t("backup.missing_firewall_plan"), sub: t("backup.missing_firewall_plan_sub"), unfit: null, start: newFirewallPlanPath(firewallIdOf(source.key)), foot: null };
  }
  if (scopeOf(source) === null) {
    return { text: t("backup.missing_volume"), sub: null, unfit: null, start: null, foot: null };
  }
  const unfit = sourceUnfit(t, source, overview.machines);
  if (unfit !== null) {
    return { text: unfit.reason, sub: null, unfit, start: null, foot: null };
  }
  const ready = overview.targets.find((target) => target.kind === "restic" && target.online && target.state === "running" && target.machine_id !== source.machine_id);
  if (ready === undefined) {
    return { text: t("backup.missing_target"), sub: t("backup.missing_target_sub"), unfit: { reason: "", link: { to: newTargetPath, label: t("backup.make_target") } }, start: null, foot: null };
  }
  return {
    text: t("backup.missing_ready", ready.name),
    sub: t("backup.missing_ready_sub", source.machine_name),
    unfit: null,
    start: `${newResticBackupPath}?source=${encodeURIComponent(source.key)}`,
    foot: t("backup.every_night_to", ready.name),
  };
}

// Une façon dont la source est sauvegardée : vue chez Proxmox, archive
// chiffrée d'openCloud, ou restic.
function ProtectionBlock({ source, protection, machines }: { source: BackupSource; protection: BackupProtection; machines: readonly BackupMachine[] }) {
  const t = useT();
  const action = useAction();
  const [deleting, setDeleting] = useState(false);
  const menu = protectionMenu(t, source, protection, {
    run: () => action.run(() => runResticBackup(source.machine_id, protection.id), { topic: "backups" }),
    verify: () => action.run(() => verifyResticBackup(source.machine_id, protection.id), { topic: "backups" }),
    remove: () => setDeleting(true),
  });
  // Un refus de droit se corrige sur la machine qui le manque : la source, ou la cible.
  const refusedHolder = action.error === null ? source.machine_id : holderOfRefusal(errorKey(action.error), { source: source.machine_id, target: protection.target_machine_id });
  return (
    <div className="backup-protection">
      <ProtectionFlow source={source} protection={protection} />
      <div className="backup-origin">
        <OriginPill protection={protection} />
        <span className="secondary">{originDetail(t, source, protection)}</span>
        {menu.length > 0 && <MoreMenu actions={menu} label={t("entity.more", source.name)} />}
      </div>
      <ReplaceNote source={source} protection={protection} />
      <PartsBlock parts={source.parts} results={protection.parts} />
      <ProtectionFacts source={source} protection={protection} />
      <ProtectionState source={source} protection={protection} machines={machines} />
      <Failure error={action.error} gesture={{ machineID: refusedHolder }} />
      {deleting && (
        <ConfirmDialog
          title={t("backup.restic_delete")}
          question={t("backup.restic_delete_confirm", source.name, protection.target_machine_name)}
          confirmLabel={t("backup.restic_delete")}
          onConfirm={() => action.run(() => deleteResticBackup(source.machine_id, protection.id), { topic: "backups" })}
          onClose={() => setDeleting(false)}
        />
      )}
    </div>
  );
}

function protectionMenu(t: Translate, source: BackupSource, protection: BackupProtection, gestures: { run: () => void; verify: () => void; remove: () => void }): MenuAction[] {
  if (protection.origin === "archive") {
    return [{ label: t("backup.plan_edit"), to: editPlanPath(protection.id) }];
  }
  if (protection.origin !== "restic") {
    return [];
  }
  // « Sauvegarder maintenant » reste offert pendant un passage : le serveur
  // refuse alors en le disant (restic.backup_running), sous la carte.
  return [
    { label: t("backup.run_now"), onSelect: gestures.run },
    { label: t("backup.verify_now"), onSelect: gestures.verify },
    { label: t("backup.restic_edit"), to: editResticPath(source.machine_id, protection.id) },
    { label: t("backup.restic_delete"), danger: true, onSelect: gestures.remove },
  ];
}

function ProtectionFlow({ source, protection }: { source: BackupSource; protection: BackupProtection }) {
  const t = useT();
  const icon = sourceIcons[source.kind];
  if (protection.origin === "observed") {
    const storage = protection.storage === "" ? protection.archive_storage : protection.storage;
    return <Flow from={source.name} fromIcon={icon} fromSub={t("backup.flow_by", source.machine_name)} label={t("backup.flow_vzdump")} to={storage} toSub={t("backup.flow_storage")} tone={stateTone(protection, "muted")} />;
  }
  if (protection.origin === "archive") {
    // Un pare-feu part de lui-même : la machine openCloud le lit, elle n'en est pas la source.
    const isFirewall = source.kind === "firewall";
    return <Flow from={isFirewall ? source.name : source.machine_name} fromIcon={icon} fromSub={isFirewall ? t("backup.flow_read_by", source.machine_name) : t("backup.flow_source")} label={t("backup.flow_archive")} to={protection.target_machine_name} toSub={protection.storage} tone={stateTone(protection, "accent")} />;
  }
  const relay = protection.path?.path === "relay";
  const label = protection.path === null ? t("backup.flow_restic") : t(`backup.path_${protection.path.path}`);
  return <Flow from={source.machine_name} fromIcon={icon} fromSub={t("backup.flow_source")} label={label} to={protection.target_machine_name} toSub={t("backup.flow_rest_server")} tone={stateTone(protection, relay ? "warn" : "accent")} />;
}

// Le trait du trajet : rouge sur un échec, sinon le ton de l'origine.
function stateTone(protection: BackupProtection, healthy: "muted" | "accent" | "warn"): "muted" | "accent" | "warn" | "danger" {
  return protection.state === "failed" || protection.state === "refused" ? "danger" : healthy;
}

function OriginPill({ protection }: { protection: BackupProtection }) {
  const t = useT();
  if (protection.origin === "observed") {
    return (
      <Pill small dot={false} tone="neutral" title={t("backup.origin_observed_title")}>
        {t("backup.origin_observed")}
      </Pill>
    );
  }
  return (
    <Pill small dot={false} tone="accent">
      {t(`backup.origin_${protection.origin}`)}
    </Pill>
  );
}

function originDetail(t: Translate, source: BackupSource, protection: BackupProtection): string {
  if (protection.origin === "observed") {
    return protection.name === "" ? t("backup.origin_observed_no_job") : t("backup.origin_job", protection.name);
  }
  if (protection.origin === "archive") {
    return protection.name === "" ? (source.kind === "firewall" ? source.name : t("backup.source_kind_database")) : protection.name;
  }
  return t(protection.scope === "vm" ? "backup.origin_restic_vm" : "backup.scope_service");
}

function ProtectionFacts({ source, protection }: { source: BackupSource; protection: BackupProtection }) {
  const { t, language } = useI18n();
  const now = useNow(30_000);
  const retention = retentionView(t, protection, source.machine_name);
  const place = protection.origin === "observed" ? protection.archive_storage || protection.storage : protection.target_machine_name;
  return (
    <dl className="backup-facts">
      <Fact label={isBroken(protection) ? t("backup.fact_last_attempt") : t("backup.fact_last")} {...lastFact(t, protection, now)} />
      {isBroken(protection) && <Fact label={t("backup.fact_last_success")} {...lastSuccessFact(t, protection, now)} />}
      {protection.next_at !== null && <Fact label={t("backup.fact_next")} value={upcoming(t, protection.next_at, now)} sub={formatWhen(protection.next_at, now)} />}
      {protection.size_bytes !== null && (
        <Fact label={t("backup.fact_size")} value={formatBytes(t, language, protection.size_bytes)} sub={t("backup.fact_size_sub")} mono />
      )}
      {protection.size_bytes === null && protection.added_bytes !== null && (
        <Fact label={t("backup.fact_added")} value={`+${formatBytes(t, language, protection.added_bytes)}`} sub={t("backup.fact_added_sub")} mono />
      )}
      {protection.copies !== null && <Fact label={t("backup.fact_copies")} value={protection.copies} sub={t("backup.fact_copies_sub", place)} mono />}
      <Fact label={t("backup.fact_retention")} value={retention.value} sub={retention.note} more={protection.origin === "restic" && <LastForget protection={protection} now={now} />} wide />
      {protection.verify !== null && <VerifyFact protection={protection} now={now} />}
    </dl>
  );
}

// Un échec, un refus ou un retard : la dernière réussite se lit à côté du dernier essai.
function isBroken(protection: BackupProtection): boolean {
  return protection.state === "failed" || protection.state === "refused" || protection.state === "late";
}

function lastSuccessFact(t: Translate, protection: BackupProtection, now: number): { value: string; sub: string | null } {
  if (protection.last_success_at === null) {
    return { value: t("backup.fact_last_success_never"), sub: null };
  }
  return { value: ago(t, protection.last_success_at, now), sub: formatWhen(protection.last_success_at, now) };
}

// Une sauvegarde que Proxmox fait de lui-même, sans plan openCloud : la page la
// montre « observée » et dit qu'elle est à remplacer, avec le geste.
function ReplaceNote({ source, protection }: { source: BackupSource; protection: BackupProtection }) {
  const t = useT();
  const replaced = source.protections.some((other) => other.origin === "restic");
  if (protection.origin !== "observed" || replaced || scopeOf(source) !== "vm") {
    return null;
  }
  return (
    <div className="backup-replace">
      <Icon name="refresh" />
      <span>
        <b>{t("backup.replace_title")}</b> {protection.name === "" ? t("backup.replace_text_nojob") : t("backup.replace_text", protection.name)}
      </span>
      <Button small to={`${newResticBackupPath}?source=${encodeURIComponent(source.key)}`}>
        {t("backup.replace_button")}
      </Button>
    </div>
  );
}

function lastFact(t: Translate, protection: BackupProtection, now: number): { value: string; sub: string | null } {
  if (protection.last_at === null) {
    return protection.state === "unknown" ? { value: t("backup.last_unknown"), sub: t("backup.last_unknown_sub") } : { value: t("backup.last_never"), sub: null };
  }
  return { value: ago(t, protection.last_at, now), sub: formatWhen(protection.last_at, now) };
}

// La dernière purge du dépôt par la cible ; ce qui l'a empêchée se voit.
function LastForget({ protection, now }: { protection: BackupProtection; now: number }) {
  const t = useT();
  const forget = forgetView(t, protection.retention.last_forget, now, (reason) => reasonText(t, reason));
  return <dd className={forget.tone === "warn" ? "backup-item-sub backup-item-warn" : "backup-item-sub"}>{forget.text}</dd>;
}

// Le test de restauration du dépôt : quand il a réussi et ce qu'il a relu, ou
// ce qui l'empêche, avec le geste du droit qui manque à la cible.
function VerifyFact({ protection, now }: { protection: BackupProtection; now: number }) {
  const t = useT();
  const verify = protection.verify;
  if (verify === null) {
    return null;
  }
  const view = verifyView(t, verify, now, (reason) => reasonText(t, reason));
  const blocked = verifyBlockedBy(verify);
  // Une cible qui ne peut pas tester ne le fera pas « la nuit » : la phrase d'attente se tait.
  const hasSub = view.sub !== null && view.sub !== "" && !(blocked !== null && verify.state === "");
  return (
    <Fact
      label={t("backup.fact_verify")}
      value={view.text}
      wide
      more={
        <>
          {hasSub && <dd className={view.tone === "warn" ? "backup-item-sub backup-item-warn" : "backup-item-sub"}>{view.sub}</dd>}
          {blocked !== null && (
            <dd className="backup-item-sub backup-item-warn">
              {t(`backup.verify_right_${blocked}`, protection.target_machine_name)}{" "}
              {blocked === "missing" && <MissingRightButton machineID={protection.target_machine_id} right="restic.verify" short />}
            </dd>
          )}
        </>
      }
    />
  );
}

// « dans 20 h » ; une échéance passée attend son tour.
function upcoming(t: Translate, at: string, now: number): string {
  const remaining = Date.parse(at) - now;
  return remaining > 0 ? t("backup.in_time", formatRemaining(t, remaining)) : t("backup.next_due");
}

// Ce qui se lit sous les faits : un échec et sa raison (avec le geste d'un
// droit qui manque), un passage en cours, ou le chemin de la dernière
// exécution restic, et pour un relais ce qu'il faut ouvrir.
function ProtectionState({ source, protection, machines }: { source: BackupSource; protection: BackupProtection; machines: readonly BackupMachine[] }) {
  const t = useT();
  if (protection.state === "failed" || protection.state === "refused") {
    const right = rightOfRefusal(protection.reason);
    const machineID = holderOfRefusal(protection.reason, { source: source.machine_id, target: protection.target_machine_id });
    const missing = missingSoftware(protection.reason);
    const holder = machines.find((candidate) => candidate.machine_id === (missing === "rest-server" ? protection.target_machine_id : source.machine_id));
    return (
      <div className="backup-path">
        <div className="backup-path-line">
          <Pill small tone="danger">
            {t(`backup.status_protection_${protection.state}`)}
          </Pill>
          <span>{protectionReasonText(t, protection.reason, (reason) => reasonText(t, reason)) || t("backup.reason_unreported")}</span>
        </div>
        {right !== null && <MissingRightButton machineID={machineID} right={right} short />}
        {missing !== null && holder !== undefined && <InstallBlock software={missing} machine={holder} />}
      </div>
    );
  }
  if (protection.state === "running") {
    return (
      <div className="backup-path">
        <div className="backup-path-line">
          <Pill small tone="accent">
            {t("backup.status_running")}
          </Pill>
        </div>
      </div>
    );
  }
  if (protection.origin !== "restic" || protection.path === null) {
    return null;
  }
  const relay = protection.path.path === "relay";
  return (
    <div className="backup-path">
      <div className="backup-path-line">
        <Pill small tone={relay ? "warn" : "ok"}>
          {t(`backup.path_${protection.path.path}_pill`)}
        </Pill>
        <span>{pathReasonText(t, protection.path)}</span>
      </div>
      {relay && <TokenBox value={directHint(t, protection.path)} />}
    </div>
  );
}
