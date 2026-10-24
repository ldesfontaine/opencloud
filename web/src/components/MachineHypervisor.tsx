import { Link } from "react-router";

import { removeMachineProxmox, useMachineProxmox, type ProxmoxGuest, type ProxmoxHost, type ProxmoxHosted, type ProxmoxStorage } from "../api/proxmox";
import type { Machine } from "../api/machines";
import { useAction } from "../hooks/useAction";
import { useNow } from "../hooks/useNow";
import { useI18n, useT } from "../i18n/context";
import { backupLabel, guestStatusLabel, guestTone, kindLabel, readErrorLabel, resourcesLabel, sortedGuests, sortedStorages, storagePercent } from "../lib/proxmox";
import { formatWhen } from "../lib/time";
import { formatBytes, formatDecimal, meterTone } from "../lib/units";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, Empty, EmptyCard, KeyValue, Meter, Note } from "./Card";
import { Failure } from "./Failure";
import { Pill } from "./Pill";
import { Subject, Table } from "./Table";
import "./MachineHypervisor.scss";

// L'onglet « Hyperviseur » d'une machine : l'hôte Proxmox qu'elle porte, ses VM
// et conteneurs, ses stockages. Lecture seule : rien n'est écrit dans Proxmox.
// Sans hôte déclaré, l'adresse de l'onglet mène à la déclaration.
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
      <GuestsCard host={host.data} />
      <StoragesCard storages={host.data.storages} />
    </div>
  );
}

// L'en-tête : l'adresse, la version, le dernier relevé et, s'il a échoué, la
// raison, avec de quoi modifier ou retirer l'hôte.
function HostCard({ machine, host }: { machine: Machine; host: ProxmoxHost }) {
  const t = useT();
  const now = useNow();
  const removal = useAction();
  const remove = () => {
    removal.run(() => removeMachineProxmox(machine.id), {
      confirm: t("machine.proxmox_remove_confirm", machine.name),
      topic: "machines",
    });
  };
  return (
    <Card>
      <CardHeader title={t("machine.proxmox_title")} />
      <CardBody gap={10}>
        {host.last_error !== "" && (
          <Note tone="warn">
            {readErrorLabel(t, host.last_error)} {host.last_read_at !== null && t("machine.proxmox_stale")}
          </Note>
        )}
        <KeyValue label={t("machine.proxmox_field_api")} value={host.api_url} mono />
        <KeyValue label={t("machine.proxmox_field_version")} value={host.version === "" ? "—" : host.version} mono />
        {host.node !== "" && <KeyValue label={t("machine.proxmox_field_node")} value={host.node} mono />}
        <KeyValue label={t("machine.proxmox_field_token")} value={host.token_id} mono />
        <KeyValue label={t("machine.proxmox_field_read")} value={host.last_read_at === null ? t("machine.proxmox_never_read") : formatWhen(host.last_read_at, now)} />
        <KeyValue label={t("machine.proxmox_field_certificate")} value={host.pin === "" ? t("machine.proxmox_system_trust") : t("machine.proxmox_pinned")} />
        <Failure error={removal.error} />
        <div className="cluster">
          <Button small icon="settings" to={`/machines/${machine.id}/proxmox`}>
            {t("machine.proxmox_edit")}
          </Button>
          <Button small variant="danger" icon="x" onClick={remove} disabled={removal.busy}>
            {t("machine.proxmox_remove")}
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}

function GuestsCard({ host }: { host: ProxmoxHost }) {
  const t = useT();
  const guests = sortedGuests(host.guests);
  return (
    <Card className="scroll-x">
      <CardHeader title={t("machine.proxmox_guests_title")} aside={guests.length} />
      {guests.length === 0 ? (
        <Empty text={host.last_read_at === null ? t("machine.proxmox_never_read") : t("machine.proxmox_guests_empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("machine.proxmox_col_name")}</th>
              <th className="wide-only">{t("machine.proxmox_col_kind")}</th>
              <th>{t("machine.proxmox_col_state")}</th>
              <th className="wide-only">{t("machine.proxmox_col_cpus")}</th>
              <th className="wide-only">{t("machine.proxmox_col_memory")}</th>
              <th className="wide-only">{t("machine.proxmox_col_machine")}</th>
              <th className="wide-only">{t("machine.proxmox_col_backup")}</th>
            </tr>
          </thead>
          <tbody>
            {guests.map((guest) => (
              <GuestRow key={guest.vmid} guest={guest} />
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

function GuestRow({ guest }: { guest: ProxmoxGuest }) {
  const t = useT();
  const { language } = useI18n();
  const now = useNow();
  return (
    <tr>
      <td>
        <Subject icon="server">
          <span className="svc-name">
            <span className="proxmox-name">
              {guest.name === "" ? guest.vmid : guest.name} <span className="secondary mono">{guest.vmid}</span>
            </span>
            {guest.template && <Pill tone="neutral">{t("machine.proxmox_template")}</Pill>}
          </span>
        </Subject>
      </td>
      <td className="wide-only">{kindLabel(t, guest.kind)}</td>
      <td>
        <Pill tone={guestTone(guest.status)}>{guestStatusLabel(t, guest.status)}</Pill>
      </td>
      <td className="num wide-only">{formatDecimal(language, guest.cpus, Number.isInteger(guest.cpus) ? 0 : 1)}</td>
      <td className="num wide-only">{formatBytes(t, language, guest.max_memory)}</td>
      <td className="wide-only">{guest.linked_machine_id === null ? "—" : <Link to={`/machines/${guest.linked_machine_id}`}>{guest.name}</Link>}</td>
      <td className="wide-only">{backupLabel(t, language, guest.last_backup_at, guest.last_backup_bytes, now)}</td>
    </tr>
  );
}

function StoragesCard({ storages }: { storages: ProxmoxStorage[] }) {
  const t = useT();
  const sorted = sortedStorages(storages);
  return (
    <Card className="scroll-x">
      <CardHeader title={t("machine.proxmox_storages_title")} aside={sorted.length} />
      {sorted.length === 0 ? (
        <Empty text={t("machine.proxmox_storages_empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("machine.proxmox_col_storage")}</th>
              <th>{t("machine.proxmox_col_usage")}</th>
              <th className="wide-only">{t("machine.proxmox_col_content")}</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((storage) => (
              <StorageRow key={`${storage.node}/${storage.name}`} storage={storage} />
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

function StorageRow({ storage }: { storage: ProxmoxStorage }) {
  const t = useT();
  const { language } = useI18n();
  const percent = storagePercent(storage);
  return (
    <tr>
      <td>
        <Subject icon="database">
          <span className="svc-name">
            <span className="proxmox-name">
              {storage.name} <span className="secondary mono">{storage.node}</span>
            </span>
            <span className="secondary">{storage.type}</span>
          </span>
        </Subject>
      </td>
      <td>
        {storage.active ? (
          <div className="storage-usage">
            <Meter percent={percent} tone={meterTone(percent)} />
            <span className="secondary mono">
              {formatBytes(t, language, storage.used)} / {formatBytes(t, language, storage.total)} · {percent} %
            </span>
          </div>
        ) : (
          <Pill tone="neutral">{t("machine.proxmox_inactive")}</Pill>
        )}
      </td>
      <td className="wide-only mono">{storage.content.join(", ")}</td>
    </tr>
  );
}

// Sur la fiche de la machine : de quoi déclarer un hôte, ou l'état du relevé
// de celui qu'elle porte.
export function ProxmoxCard({ machine }: { machine: Machine }) {
  const t = useT();
  const summary = machine.proxmox;
  // Une VM d'un hôte déjà déclaré ne se propose pas comme hyperviseur : son
  // encadré « Sur l'hôte Proxmox » suffit (l'adresse du formulaire reste servie).
  if (!summary.declared && summary.hosted !== null) {
    return null;
  }
  if (!summary.declared) {
    return (
      <Card>
        <CardHeader title={t("machine.proxmox_title")} />
        <CardBody gap={10}>
          <p className="secondary">{t("machine.proxmox_declare_hint")}</p>
          <div>
            <Button small icon="plus" to={`/machines/${machine.id}/proxmox`}>
              {t("machine.proxmox_declare")}
            </Button>
          </div>
        </CardBody>
      </Card>
    );
  }
  return (
    <Card>
      <CardHeader title={t("machine.proxmox_title")} />
      <CardBody gap={10}>
        {summary.last_error !== "" && <Note tone="warn">{readErrorLabel(t, summary.last_error)}</Note>}
        <KeyValue label={t("machine.proxmox_guests_title")} value={summary.guests} />
        <div>
          <Button small icon="server" to={`/machines/${machine.id}/hyperviseur`}>
            {t("machine.proxmox_open")}
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}

// Sur la fiche d'une machine qui est la VM d'un hôte : son numéro, ce qui lui
// est alloué, sa dernière sauvegarde.
export function HostedCard({ hosted }: { hosted: ProxmoxHosted }) {
  const t = useT();
  const { language } = useI18n();
  const now = useNow();
  return (
    <Card>
      <CardHeader title={t("machine.proxmox_hosted_title")} />
      <CardBody gap={10}>
        <KeyValue label={t("machine.proxmox_hosted_host")} value={<Link to={`/machines/${hosted.host_machine_id}/hyperviseur`}>{hosted.host_machine_name}</Link>} />
        <KeyValue
          label={kindLabel(t, hosted.kind)}
          value={
            <>
              <span className="proxmox-name">{hosted.name}</span> <span className="secondary mono">{hosted.vmid}</span> <Pill tone={guestTone(hosted.status)}>{guestStatusLabel(t, hosted.status)}</Pill>
            </>
          }
        />
        <KeyValue label={t("machine.proxmox_hosted_resources")} value={resourcesLabel(t, language, hosted.cpus, hosted.max_memory)} />
        <KeyValue label={t("machine.proxmox_col_backup")} value={backupLabel(t, language, hosted.last_backup_at, hosted.last_backup_bytes, now)} />
      </CardBody>
    </Card>
  );
}
