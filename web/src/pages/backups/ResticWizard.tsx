import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { errorKey } from "../../api/client";
import { createResticBackup, useBackupOverview, type BackupMachine, type BackupOverview, type BackupSource } from "../../api/backups";
import { Meter, Note } from "../../components/Card";
import { Choice } from "../../components/Choice";
import { TokenBox } from "../../components/Copy";
import { SettingsFold } from "../../components/Disclosure";
import { Input } from "../../components/Field";
import { Icon } from "../../components/Icon";
import { Modal } from "../../components/Modal";
import { PickList, type PickOption } from "../../components/PickList";
import { Pill } from "../../components/Pill";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useI18n, useT, type Translate } from "../../i18n/context";
import {
  candidatesFor,
  declaredRetention,
  defaultRetention,
  directHint,
  expectedPath,
  floorText,
  hostPort,
  matchesSearch,
  pathReasonText,
  scopeOf,
  sourceStatus,
  sourceSubtitle,
  sourceUnfit,
  targetChoices,
  type ResticScope,
  type Unfitness,
} from "../../lib/backupOverview";
import { backupsPath, newTargetPathFor } from "../../lib/backups";
import { isFormModified } from "../../lib/modal";
import { missingSoftware, type MissingSoftware } from "../../lib/resticInstall";
import { holderOfRefusal } from "../../lib/rights";
import { formatBytes, percentOf } from "../../lib/units";
import { InstallBlock } from "./InstallBlock";
import { PartsBlock } from "./Parts";
import { RetentionFields, type RetentionValues } from "./RetentionFields";
import { sourceIcons } from "./SourceCard";

const scopes: readonly ResticScope[] = ["vm", "service"];

interface ResticForm extends RetentionValues {
  scope: ResticScope;
  source: string;
  target: string;
  search: string;
}

type Form = ReturnType<typeof useForm<ResticForm>>;

// « Sauvegarder… » : la fenêtre de la page Sauvegardes, à /sauvegardes/nouvelle
// (?source= choisit d'avance). Quoi (une VM entière ou un service entier),
// où (une cible restic prête, et la rétention qu'elle appliquera), puis le
// récapitulatif : chaque nuit, la rétention et le plancher de la cible, le
// chemin attendu. Elle attend l'aperçu : elle y lit les sources, les cibles,
// les droits et ce qui manque à chaque machine.
export function ResticWizard() {
  const overview = useBackupOverview();
  return overview.data === null ? null : <ResticDialog overview={overview.data} />;
}

function ResticDialog({ overview }: { overview: BackupOverview }) {
  const t = useT();
  const navigate = useNavigate();
  const [search] = useSearchParams();
  const [initial] = useState(() => initialForm(t, overview, search.get("source")));
  const form = useForm(initial);
  const action = useAction();
  const { values } = form;
  const source = overview.sources.find((candidate) => candidate.key === values.source);

  const close = () => void navigate(backupsPath, { replace: true });
  const save = () => {
    if (source === undefined) {
      return;
    }
    const request = {
      name: source.name,
      scope: values.scope,
      vmid: source.vmid ?? 0,
      service_id: source.service_id ?? "",
      target_machine_id: values.target,
      retention: { ...defaultRetention, keep_daily: Number(values.keep_daily), keep_weekly: Number(values.keep_weekly), keep_monthly: Number(values.keep_monthly) },
    };
    action.run(() => createResticBackup(source.machine_id, request), { topic: "backups", onDone: close });
  };
  // Un refus de droit mène à la machine qui le manque : la cible pour
  // restic.target, la source sinon.
  const refused = action.error === null ? "" : errorKey(action.error);
  const gestureMachine = holderOfRefusal(refused, { source: source?.machine_id ?? "", target: values.target });
  // Un logiciel qui manque mène au même bloc que la carte : sur la cible pour
  // rest-server, sur la source pour restic.
  const missing = missingSoftware(refused);
  const lacking = overview.machines.find((machine) => machine.machine_id === (missing === "rest-server" ? values.target : source?.machine_id));

  return (
    <Modal title={t("backup.restic_add_title")} onClose={close} dirty={isFormModified({ ...values, search: "" }, { ...initial, search: "" })} wide>
      <Wizard
        steps={stepsOf(t, form, overview)}
        mode="create"
        onSubmit={save}
        submitLabel={t("backup.restic_submit")}
        submitIcon="archive"
        busy={action.busy}
        error={action.error}
        errorGesture={gestureMachine === "" ? undefined : { machineID: gestureMachine }}
        errorAside={missing !== null && lacking !== undefined ? <InstallBlock software={missing} machine={lacking} framed /> : undefined}
      />
    </Modal>
  );
}

// Le départ : la source demandée si elle convient, sinon la première qui
// convient (les services d'abord), et la première cible prête pour elle.
function initialForm(t: Translate, overview: BackupOverview, wanted: string | null): ResticForm {
  const asked = overview.sources.find((candidate) => candidate.key === wanted);
  const scope = (asked === undefined ? null : scopeOf(asked)) ?? "service";
  const source = asked !== undefined && sourceUnfit(t, asked, overview.machines) === null ? asked.key : firstFitSource(t, overview, scope);
  return {
    scope,
    source,
    target: firstFitTarget(t, overview, source),
    search: "",
    keep_daily: String(defaultRetention.keep_daily),
    keep_weekly: String(defaultRetention.keep_weekly),
    keep_monthly: String(defaultRetention.keep_monthly),
  };
}

// Une source que rien d'openCloud ne protège passe avant une déjà protégée.
function firstFitSource(t: Translate, overview: BackupOverview, scope: ResticScope): string {
  const fit = candidatesFor(overview.sources, scope).filter((candidate) => sourceUnfit(t, candidate, overview.machines) === null);
  const unprotected = fit.find((candidate) => !candidate.protections.some((protection) => protection.origin !== "observed"));
  return (unprotected ?? fit[0])?.key ?? "";
}

function firstFitTarget(t: Translate, overview: BackupOverview, sourceKey: string): string {
  const machineID = overview.sources.find((candidate) => candidate.key === sourceKey)?.machine_id ?? "";
  return targetChoices(t, overview, machineID, newTargetPathFor).find((choice) => choice.unfit === null)?.machine.machine_id ?? "";
}

function stepsOf(t: Translate, form: Form, overview: BackupOverview): WizardStep[] {
  const { values, update } = form;
  const source = overview.sources.find((candidate) => candidate.key === values.source);
  const target = overview.machines.find((machine) => machine.machine_id === values.target);
  const chosenTarget = overview.targets.find((candidate) => candidate.kind === "restic" && candidate.machine_id === values.target);
  const choices = targetChoices(t, overview, source?.machine_id ?? "", newTargetPathFor);
  const listed = listedSources(t, overview, values);
  // Changer de source garde la cible si elle lui convient encore.
  const pickSource = (key: string) => {
    const machineID = overview.sources.find((candidate) => candidate.key === key)?.machine_id ?? "";
    const stillFits = targetChoices(t, overview, machineID, newTargetPathFor).some((choice) => choice.unfit === null && choice.machine.machine_id === values.target);
    update({ source: key, target: stillFits ? values.target : firstFitTarget(t, overview, key) });
  };
  const pickScope = (scope: ResticScope) => {
    const first = firstFitSource(t, overview, scope);
    update({ scope, source: first, target: firstFitTarget(t, overview, first), search: "" });
  };
  return [
    {
      id: "what",
      title: t("backup.restic_step_what"),
      validate: () => (source === undefined ? "backup.restic_pick_source" : null),
      render: () => (
        <>
          <div>
            <Choice label={t("backup.restic_scope_label")} name="restic-scope" choices={scopes} value={values.scope} text={(scope) => t(`backup.restic_scope_${scope}`)} onChange={pickScope} />
          </div>
          <label className="backup-search">
            <Icon name="search" />
            <Input
              id="restic-search"
              type="search"
              autoComplete="off"
              aria-label={t("backup.restic_search_label")}
              placeholder={t(`backup.restic_search_${values.scope}`)}
              value={values.search}
              onChange={(event) => form.change("search", event.target.value)}
            />
          </label>
          <PickList name="restic-source" label={t("backup.restic_step_what")} options={sourceOptions(t, listed)} value={values.source} onChange={pickSource} empty={t(`backup.restic_none_${values.scope}`)} />
          <InstallBlocks unfits={listed.map((item) => item.unfit)} />
          {source !== undefined && scopeOf(source) === "service" && <PartsBlock parts={source.parts} results={[]} />}
          <Note tone="neutral">{t(`backup.restic_scope_note_${values.scope}`)}</Note>
        </>
      ),
      summary: () => (source === undefined ? [] : [{ label: t("backup.restic_summary_what"), value: <SummaryValue main={source.name} detail={whatDetail(t, source)} /> }]),
    },
    {
      id: "where",
      title: t("backup.restic_step_where"),
      validate: () => (target === undefined ? "backup.restic_pick_target" : null),
      // Aucune cible prête : la liste dit pourquoi, Suivant attend.
      blocked: () => choices.every((choice) => choice.unfit !== null),
      render: () => (
        <>
          <PickList name="restic-target" label={t("backup.restic_step_where")} options={targetOptions(t, choices)} value={values.target} onChange={(id) => update({ target: id })} />
          <InstallBlocks unfits={choices.map((choice) => choice.unfit)} />
          {/* Une rétention déjà changée se rouvre : on revient pour elle. */}
          <SettingsFold hint={t("backup.restic_settings_hint", retentionOf(t, values))} initiallyOpen={!isDefaultRetention(values)}>
            <RetentionFields values={values} onChange={(key, value) => form.change(key, value)} target={chosenTarget} />
          </SettingsFold>
          {source !== undefined && target !== undefined && (
            <Note tone="neutral">
              <span className="backup-note-line">
                <Icon name="lock" />
                {t("backup.restic_append_only", target.machine_name, source.name)}
              </span>
            </Note>
          )}
        </>
      ),
      summary: () => (source === undefined || target === undefined ? [] : whereSummary(t, overview, source, target.machine_id, values)),
    },
  ];
}

// La liste de l'étape « Quoi » : la portée, filtrée par la recherche (sans
// casse ni accent), chaque source avec ce qui la protège déjà.
function listedSources(t: Translate, overview: BackupOverview, values: ResticForm): { candidate: BackupSource; unfit: Unfitness | null }[] {
  return candidatesFor(overview.sources, values.scope)
    .filter((candidate) => matchesSearch(candidate, values.search) || candidate.key === values.source)
    .map((candidate) => ({ candidate, unfit: sourceUnfit(t, candidate, overview.machines) }))
    // Ce qui peut partir d'abord ; le reste, grisé, avec son geste.
    .sort((a, b) => Number(a.unfit !== null) - Number(b.unfit !== null));
}

function sourceOptions(t: Translate, listed: ReturnType<typeof listedSources>): PickOption[] {
  return listed.map(({ candidate, unfit }) => ({
      value: candidate.key,
      icon: sourceIcons[candidate.kind],
      name: candidate.name,
      detail: sourceSubtitle(t, candidate),
      end: <ProtectionPill source={candidate} />,
      unfit,
    }));
}

// Ce qui protège déjà une source : Proxmox de lui-même, openCloud, ou rien.
function ProtectionPill({ source }: { source: BackupSource }) {
  const t = useT();
  if (source.protections.some((protection) => protection.origin === "restic" || protection.origin === "archive")) {
    return (
      <Pill small tone="ok">
        {t("backup.restic_protected")}
      </Pill>
    );
  }
  if (source.protections.some((protection) => protection.origin === "observed")) {
    return (
      <Pill small dot={false} tone="neutral">
        {t("backup.origin_observed")}
      </Pill>
    );
  }
  return (
    <Pill small tone={sourceStatus(source) === "unknown" ? "neutral" : "warn"}>
      {t(sourceStatus(source) === "unknown" ? "backup.status_unknown" : "backup.restic_unprotected")}
    </Pill>
  );
}

// Sous une liste, les commandes d'installation de chaque machine grisée
// faute de restic ou de rest-server, une fois par machine.
function InstallBlocks({ unfits }: { unfits: readonly (Unfitness | null)[] }) {
  const shown = new Map<string, { software: MissingSoftware; machine: BackupMachine }>();
  for (const unfit of unfits) {
    if (unfit?.install !== undefined) {
      shown.set(`${unfit.install.software}:${unfit.install.machine.machine_id}`, unfit.install);
    }
  }
  return [...shown.entries()].map(([key, install]) => <InstallBlock key={key} software={install.software} machine={install.machine} framed />);
}

function targetOptions(t: Translate, choices: ReturnType<typeof targetChoices>): PickOption[] {
  return choices.map((choice) => ({
    value: choice.machine.machine_id,
    icon: "server",
    name: choice.machine.machine_name,
    detail: choice.target === null ? "" : <TargetDetail target={choice.target} />,
    end:
      choice.target === null ? undefined : (
        <>
          {choice.target.disk_total_bytes > 0 && <TargetMeter used={choice.target.disk_used_bytes} total={choice.target.disk_total_bytes} />}
          <Pill small tone="ok">
            {t("backup.restic_target_ready")}
          </Pill>
        </>
      ),
    unfit: choice.unfit,
  }));
}

function TargetDetail({ target }: { target: NonNullable<ReturnType<typeof targetChoices>[number]["target"]> }) {
  const { t, language } = useI18n();
  if (target.disk_total_bytes <= 0) {
    return t("backup.restic_target_running");
  }
  return t("backup.restic_target_disk", formatBytes(t, language, target.disk_free_bytes), formatBytes(t, language, target.disk_total_bytes));
}

function SummaryValue({ main, detail }: { main: string; detail: string }) {
  return (
    <span className="backup-summary-value">
      <b>{main}</b>
      <span className="secondary">{detail}</span>
    </span>
  );
}

function whatDetail(t: Translate, source: BackupSource): string {
  return scopeOf(source) === "vm" ? t("backup.restic_what_vm", source.machine_name) : t("backup.restic_what_service", source.machine_name);
}

function isDefaultRetention(values: RetentionValues): boolean {
  return (
    Number(values.keep_daily) === defaultRetention.keep_daily && Number(values.keep_weekly) === defaultRetention.keep_weekly && Number(values.keep_monthly) === defaultRetention.keep_monthly
  );
}

// La rétention saisie, en clair : « 14 nuits · 8 semaines · 3 mois ».
function retentionOf(t: Translate, values: RetentionValues): string {
  const kept = { keep_daily: Number(values.keep_daily), keep_weekly: Number(values.keep_weekly), keep_monthly: Number(values.keep_monthly) };
  return declaredRetention(t, { known: true, applied: true, keep_all: false, keep_last: 0, keep_yearly: 0, last_forget: null, ...kept }) ?? t("backup.retention_all");
}

// Le récapitulatif de « Où » : la cible, le rythme, la rétention et le
// plancher de la cible, le chemin attendu, d'où viennent les mots de passe.
function whereSummary(t: Translate, overview: BackupOverview, source: BackupSource, targetID: string, values: RetentionValues) {
  const target = overview.targets.find((candidate) => candidate.kind === "restic" && candidate.machine_id === targetID);
  const name = overview.machines.find((machine) => machine.machine_id === targetID)?.machine_name ?? "";
  const address = target === undefined || target.host === "" ? "" : hostPort(target.host, target.port);
  const floor = target?.retention_floor == null ? null : floorText(t, target.retention_floor);
  const applied = floor === null ? t("backup.retention_applied", name) : `${t("backup.retention_applied", name)} ${t("backup.retention_floor_of", name, floor)}`;
  return [
    { label: t("backup.restic_summary_where"), value: <SummaryValue main={name} detail={t("backup.restic_summary_where_detail", address)} /> },
    { label: t("backup.restic_summary_rhythm"), value: <SummaryValue main={t("backup.restic_every_night")} detail={t("backup.restic_every_night_detail")} /> },
    { label: t("backup.fact_retention"), value: <SummaryValue main={retentionOf(t, values)} detail={applied} /> },
    { label: t("backup.restic_summary_path"), value: <ExpectedPath overview={overview} source={source} targetID={targetID} address={address} /> },
    { label: t("backup.restic_summary_passwords"), value: <span className="secondary">{t("backup.restic_passwords")}</span> },
  ];
}

// Le chemin que prendra la sauvegarde : celui de la dernière qui va de la
// même source à la même cible ; sinon la règle (direct si elle la joint).
function ExpectedPath({ overview, source, targetID, address }: { overview: BackupOverview; source: BackupSource; targetID: string; address: string }) {
  const t = useT();
  const known = expectedPath(overview.sources, source.machine_id, targetID);
  if (known === null) {
    return <span className="secondary">{t("backup.restic_path_rule", source.machine_name, address)}</span>;
  }
  const relay = known.path === "relay";
  return (
    <span className="backup-summary-path">
      <span className="backup-path-line">
        <Pill small tone={relay ? "warn" : "ok"}>
          {t(`backup.path_${known.path}_pill`)}
        </Pill>
        <span>{pathReasonText(t, known)}</span>
      </span>
      {relay && <TokenBox value={directHint(t, known)} />}
    </span>
  );
}

// Une jauge discrète dans la ligne d'une cible : la part du disque prise.
function TargetMeter({ used, total }: { used: number; total: number }) {
  return (
    <span className="backup-mini">
      <Meter percent={percentOf(used, total)} />
    </span>
  );
}
