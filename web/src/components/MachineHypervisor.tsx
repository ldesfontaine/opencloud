import { useRef, useState } from "react";
import { Link } from "react-router";

import { useMachineActions, type ActionRun } from "../api/actions";
import { actOnGuest, refreshMachineProxmox, removeMachineProxmox, useMachineProxmox, type ProxmoxGuest, type ProxmoxHost, type ProxmoxStorage } from "../api/proxmox";
import type { Machine } from "../api/machines";
import { useAction } from "../hooks/useAction";
import { useNow } from "../hooks/useNow";
import { useI18n, useT } from "../i18n/context";
import {
  allocationShares,
  backupTone,
  gestureBlockText,
  gestureLabel,
  gestureQuestion,
  gestureStep,
  gestureStepTones,
  guestGestures,
  guestStatusLabel,
  groupGuests,
  guestTone,
  kindLabel,
  readErrorLabel,
  sortedStorages,
  storageBackupState,
  storagePercent,
  type GuestGesture,
} from "../lib/proxmox";
import { capabilityLabelKey } from "../lib/machines";
import { reenrollPath } from "../lib/rights";
import { hintText } from "../lib/credential";
import { ago, formatWhen } from "../lib/time";
import { formatBytes, formatDecimal, meterTone } from "../lib/units";
import { reasonText } from "./ActionsCard";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, Empty, EmptyCard, Note } from "./Card";
import { ConfirmInline } from "./ConfirmInline";
import { ConfirmModal } from "./ConfirmModal";
import { TokenBox } from "./Copy";
import { Fold } from "./Disclosure";
import { CardGrid, EntityCard } from "./EntityCard";
import { Failure } from "./Failure";
import type { MenuAction } from "./MoreMenu";
import { Pill } from "./Pill";
import "./MachineHypervisor.scss";

// L'onglet « Hyperviseur » d'une machine : l'hôte Proxmox qu'elle porte, ses VM
// et conteneurs, ses stockages. Le serveur n'écrit rien dans Proxmox : les
// gestes sur un invité (démarrer, arrêter, instantanés) sont des ordres à
// l'agent de l'hôte, sous ses droits proxmox.*. Sans hôte déclaré, l'adresse de l'onglet mène à la déclaration.
export function MachineHypervisor({ machine }: { machine: Machine }) {
  const t = useT();
  const declared = machine.proxmox.declared;
  const host = useMachineProxmox(machine.id, declared);
  if (!declared) {
    return (
      <EmptyCard
        icon="server"
        text={t("machine.proxmox_not_declared")}
        action={
          <Button icon="plus" to={`/machines/${machine.id}/proxmox`}>
            {t("machine.proxmox_declare")}
          </Button>
        }
      />
    );
  }
  if (host.error && !host.data) {
    return <Failure error={host.error} />;
  }
  if (!host.data) {
    return null;
  }
  return (
    <div className="hypervisor">
      {host.error && <Failure error={host.error} />}
      <HostCard machine={machine} host={host.data} />
      <BackupAccessCard machine={machine} host={host.data} />
      <StoragesCard host={host.data} />
      <GuestsCard machine={machine} host={host.data} />
    </div>
  );
}

// L'en-tête : l'adresse, la version, le dernier relevé en une bande de faits et,
// si le relevé a échoué, la raison, avec de quoi modifier ou retirer l'hôte.
function HostCard({ machine, host }: { machine: Machine; host: ProxmoxHost }) {
  const t = useT();
  const now = useNow();
  const removal = useAction();
  const remove = () => {
    removal.run(() => removeMachineProxmox(machine.id), { topic: "machines" });
  };
  const facts = [
    { label: t("machine.proxmox_field_api"), value: host.api_url, mono: true },
    { label: t("machine.proxmox_field_version"), value: host.version === "" ? "—" : host.version, mono: true },
    ...(host.node === "" ? [] : [{ label: t("machine.proxmox_field_node"), value: host.node, mono: true }]),
    { label: t("machine.proxmox_field_token"), value: hintText(t, host.token_id_hint), mono: true },
    { label: t("machine.proxmox_field_read"), value: host.last_read_at === null ? t("machine.proxmox_never_read") : formatWhen(host.last_read_at, now), mono: false },
    { label: t("machine.proxmox_field_certificate"), value: host.pin === "" ? t("machine.proxmox_system_trust") : t("machine.proxmox_pinned"), mono: false },
  ];
  return (
    <Card>
      <CardHeader title={t("machine.proxmox_title")} />
      <CardBody gap={12}>
        {host.last_error !== "" && (
          <Note tone="warn">
            {readErrorLabel(t, host.last_error)} {host.last_read_at !== null && t("machine.proxmox_stale")}
          </Note>
        )}
        <dl className="host-facts">
          {facts.map((fact) => (
            <div key={fact.label}>
              <dt>{fact.label}</dt>
              <dd className={fact.mono ? "mono" : undefined}>{fact.value}</dd>
            </div>
          ))}
        </dl>
        <Failure error={removal.error} />
        <div className="cluster">
          <Button small icon="settings" to={`/machines/${machine.id}/proxmox`}>
            {t("machine.proxmox_edit")}
          </Button>
          <ConfirmModal
            title={t("machine.proxmox_remove")}
            question={t("machine.proxmox_remove_confirm", machine.name)}
            confirmLabel={t("machine.proxmox_remove")}
            onConfirm={remove}
            variant="danger"
            small
            icon="x"
            disabled={removal.busy}
          >
            {t("machine.proxmox_remove")}
          </ConfirmModal>
        </div>
      </CardBody>
    </Card>
  );
}

// Le jeton liste les VM mais pas les archives de certains stockages : Proxmox
// répond alors « aucune » sans erreur (voir internal/proxmox/backup_access.go).
// La carte le dit, avec les commandes exactes à coller, au lieu de laisser
// afficher « Aucune ».
function BackupAccessCard({ machine, host }: { machine: Machine; host: ProxmoxHost }) {
  const t = useT();
  const refreshing = useAction();
  const blind = host.backup_blind_storages;
  if (blind.length === 0) {
    return null;
  }
  const grant = host.backup_grant;
  const refresh = () => {
    refreshing.run(() => refreshMachineProxmox(machine.id), { topic: "machines" });
  };
  return (
    <Card className="backup-access">
      <CardHeader title={t("machine.backup_blind_title")} />
      <CardBody gap={12}>
        <p className="secondary">{t("machine.backup_blind_text", blind.join(", "))}</p>
        {grant === null ? (
          <Note tone="warn">{t("machine.backup_blind_manual", blind.join(", "))}</Note>
        ) : (
          <>
            <div className="backup-step">
              <strong>{t("machine.backup_blind_step_role")}</strong>
              <TokenBox value={grant.commands[0] ?? ""} />
            </div>
            <div className="backup-step">
              <strong>{t("machine.backup_blind_step_acl")}</strong>
              {grant.commands.slice(1).map((command) => (
                <TokenBox key={command} value={command} />
              ))}
            </div>
            <p className="secondary">{t("machine.backup_blind_cost")}</p>
          </>
        )}
        <Failure error={refreshing.error} />
        <div className="cluster">
          <Button small icon="refresh" onClick={refresh} disabled={refreshing.busy}>
            {t("machine.backup_blind_refresh")}
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}

// Les invités de l'hôte en cartes : ceux qui tournent d'abord, les arrêtées
// repliées, les modèles à part. Les ordres de la machine se lisent une fois pour
// la carte : chaque invité y suit le sien.
function GuestsCard({ machine, host }: { machine: Machine; host: ProxmoxHost }) {
  const t = useT();
  const groups = groupGuests(host.guests);
  const runs = useMachineActions(machine.id);
  const card = (guest: ProxmoxGuest) => <GuestCard key={guest.vmid} machine={machine} guest={guest} among={host.guests} runs={runs.data?.runs ?? []} />;
  const count = groups.live.length + groups.stopped.length;
  return (
    <Card>
      <CardHeader title={t("machine.proxmox_guests_title")} aside={t("machine.proxmox_guests_summary", groups.live.length, groups.stopped.length)} />
      {host.guests.length === 0 ? (
        <Empty text={host.last_read_at === null ? t("machine.proxmox_never_read") : t("machine.proxmox_guests_empty")} />
      ) : (
        <CardBody gap={16}>
          {count > 0 && <p className="secondary">{t("machine.proxmox_allocated_note")}</p>}
          {groups.live.length > 0 && <CardGrid>{groups.live.map(card)}</CardGrid>}
          {groups.stopped.length > 0 && (
            <Fold label={t("machine.proxmox_stopped")} hint={String(groups.stopped.length)} initiallyOpen={groups.live.length === 0}>
              <CardGrid>{groups.stopped.map(card)}</CardGrid>
            </Fold>
          )}
          {groups.templates.length > 0 && (
            <Fold label={t("machine.proxmox_templates")} hint={t("machine.proxmox_templates_hint", groups.templates.length)} initiallyOpen={false}>
              <ul className="template-list">
                {groups.templates.map((guest) => (
                  <li key={guest.vmid}>
                    <Pill tone="neutral" dot={false}>
                      {guest.name === "" ? guest.vmid : `${guest.name} · ${guest.vmid}`}
                    </Pill>
                  </li>
                ))}
              </ul>
            </Fold>
          )}
        </CardBody>
      )}
    </Card>
  );
}

// La carte d'un invité : son état, ses ressources allouées en jauges, la machine
// enrôlée qui le porte et sa dernière sauvegarde, et au menu « … » ses gestes.
function GuestCard({ machine, guest, among, runs }: { machine: Machine; guest: ProxmoxGuest; among: ProxmoxGuest[]; runs: ActionRun[] }) {
  const t = useT();
  const { language } = useI18n();
  const now = useNow();
  const sending = useAction();
  const panel = useRef<HTMLDivElement>(null);
  // Le geste choisi au menu, en attente de confirmation ; puis le run envoyé.
  const [chosen, setChosen] = useState<GuestGesture | null>(null);
  const [sent, setSent] = useState<{ gesture: GuestGesture; run: ActionRun } | null>(null);
  const name = guest.name === "" ? String(guest.vmid) : guest.name;
  const gestures = guestGestures(guest, machine);
  const shares = allocationShares(guest, among);

  const choose = (gesture: GuestGesture) => {
    setSent(null);
    setChosen(gesture);
  };
  const cancel = () => {
    setChosen(null);
    panel.current?.closest("article")?.querySelector<HTMLButtonElement>(".more-menu > button")?.focus();
  };
  const send = (gesture: GuestGesture) => {
    sending.run(() => actOnGuest(machine.id, guest.vmid, gesture.verb, gesture.snapshot), {
      topic: "actions",
      onDone: (run) => setSent({ gesture, run }),
      onSettled: () => setChosen(null),
    });
  };

  const actions: MenuAction[] = gestures.map((gesture) => ({
    label: gestureLabel(t, gesture),
    danger: gesture.verb === "snapshot-delete",
    disabled: gesture.blocked !== null,
    ...(gesture.blocked !== null && { hint: gestureBlockText(t, gesture.blocked, machine.agent) }),
    onSelect: () => choose(gesture),
  }));
  // Un droit qui manque se donne d'ici : ré-enrôler l'agent, droits pré-cochés.
  const missing = gestures.find((gesture) => gesture.blocked?.kind === "right_missing")?.blocked;
  if (missing?.kind === "right_missing") {
    actions.push({ label: t("machine.reenroll_with", t(capabilityLabelKey(missing.right))), to: reenrollPath(machine.id, { name: missing.right }) });
  }
  const panelOpen = chosen !== null || sent !== null || sending.error !== null;
  const backup = { at: guest.last_backup_at, bytes: guest.last_backup_bytes, unknown: guest.backup_unknown };

  return (
    <EntityCard
      {...(guest.linked_machine_id !== null && { to: `/machines/${guest.linked_machine_id}` })}
      icon={guest.kind === "lxc" ? "box" : "server"}
      name={name}
      kind={`${kindLabel(t, guest.kind)} · ${guest.vmid}`}
      state={<Pill tone={guestTone(guest.status)} small>{guestStatusLabel(t, guest.status)}</Pill>}
      facts={[
        {
          label: t("machine.proxmox_col_machine"),
          mono: false,
          wide: true,
          value: guest.linked_machine_id === null ? <span className="secondary">{t("machine.proxmox_unlinked")}</span> : <Link to={`/machines/${guest.linked_machine_id}`}>{guest.name}</Link>,
        },
        {
          label: t("machine.proxmox_col_backup"),
          mono: false,
          wide: true,
          value: (
            <span className="backup-fact">
              <Pill tone={backupTone(backup, now)} small title={guest.last_backup_at === null ? undefined : formatWhen(guest.last_backup_at, now)}>
                {guest.backup_unknown ? t("machine.proxmox_backup_unknown") : guest.last_backup_at === null ? t("machine.proxmox_no_backup") : ago(t, guest.last_backup_at, now)}
              </Pill>
              {guest.last_backup_at !== null && (
                <span className="secondary mono">{[formatBytes(t, language, guest.last_backup_bytes), guest.last_backup_storage].filter((part) => part !== "").join(" · ")}</span>
              )}
            </span>
          ),
        },
      ]}
      meters={[
        { label: t("machine.proxmox_meter_cpus"), value: t("machine.proxmox_vcpus", formatDecimal(language, guest.cpus, Number.isInteger(guest.cpus) ? 0 : 1)), percent: shares.cpus },
        { label: t("machine.proxmox_meter_memory"), value: formatBytes(t, language, guest.max_memory), percent: shares.memory },
        ...(guest.max_disk > 0 ? [{ label: t("machine.proxmox_meter_disk"), value: formatBytes(t, language, guest.max_disk), percent: shares.disk }] : []),
      ]}
      actions={actions}
    >
      {panelOpen && (
        <div className="guest-gesture" ref={panel}>
          <Failure error={sending.error} />
          {chosen !== null && (
            <ConfirmInline asking question={gestureQuestion(t, chosen, name)} onConfirm={() => send(chosen)} onCancel={cancel} disabled={sending.busy}>
              {gestureLabel(t, chosen)}
            </ConfirmInline>
          )}
          {sent !== null && <GestureProgress label={gestureLabel(t, sent.gesture)} run={runs.find((run) => run.id === sent.run.id) ?? sent.run} onDismiss={() => setSent(null)} />}
        </div>
      )}
    </EntityCard>
  );
}

// Où en est le geste envoyé, relu par le direct (« actions ») : envoyé, accepté
// par l'agent, fait ou échoué, avec la raison et la sortie de l'échec.
function GestureProgress({ label, run, onDismiss }: { label: string; run: ActionRun; onDismiss: () => void }) {
  const t = useT();
  const step = gestureStep(run.state);
  const finished = step === "done" || step === "failed";
  const output = run.output.map((line) => line.line).join("\n");
  return (
    <div className="gesture-progress" role="status">
      <div className="gesture-progress-head">
        <span>{label}</span>
        <Pill tone={gestureStepTones[step]}>{t(`machine.proxmox_progress_${step}`)}</Pill>
        {finished && (
          <Button small variant="ghost" onClick={onDismiss}>
            {t("machine.proxmox_progress_dismiss")}
          </Button>
        )}
      </div>
      {step === "failed" && run.reason !== "" && <p className="secondary">{reasonText(t, run.reason)}</p>}
      {step === "failed" && output !== "" && <pre className="secondary mono gesture-output">{output}</pre>}
    </div>
  );
}

// Les stockages en cartes, avec leur remplissage en jauge. Un stockage qui
// reçoit les sauvegardes le dit ; si le jeton ne sait pas en lister les archives,
// la pastille le dit aussi.
function StoragesCard({ host }: { host: ProxmoxHost }) {
  const t = useT();
  const sorted = sortedStorages(host.storages);
  return (
    <Card>
      <CardHeader title={t("machine.proxmox_storages_title")} aside={sorted.length} />
      {sorted.length === 0 ? (
        <Empty text={t("machine.proxmox_storages_empty")} />
      ) : (
        <CardBody>
          <CardGrid>
            {sorted.map((storage) => (
              <StorageCard key={`${storage.node}/${storage.name}`} storage={storage} blind={host.backup_blind_storages} />
            ))}
          </CardGrid>
        </CardBody>
      )}
    </Card>
  );
}

function StorageCard({ storage, blind }: { storage: ProxmoxStorage; blind: string[] }) {
  const t = useT();
  const { language } = useI18n();
  const percent = storagePercent(storage);
  const backups = storageBackupState(storage, blind);
  return (
    <EntityCard
      icon="database"
      name={storage.name}
      kind={`${storage.type} · ${storage.node}`}
      state={
        !storage.active ? (
          <Pill tone="neutral" small>
            {t("machine.proxmox_inactive")}
          </Pill>
        ) : backups === "blind" ? (
          <Pill tone="warn" small>
            {t("machine.proxmox_storage_blind")}
          </Pill>
        ) : backups === "readable" ? (
          <Pill tone="accent" small>
            {t("machine.proxmox_storage_receives")}
          </Pill>
        ) : undefined
      }
      facts={[{ label: t("machine.proxmox_col_content"), value: storage.content.join(", ") || "—", wide: true }]}
      meters={
        storage.active
          ? [{ label: t("machine.proxmox_storage_used"), value: `${formatBytes(t, language, storage.used)} / ${formatBytes(t, language, storage.total)} · ${percent} %`, percent, tone: meterTone(percent) }]
          : []
      }
    />
  );
}
