import { Link } from "react-router";

import type { Machine } from "../api/machines";
import { useNow } from "../hooks/useNow";
import { useI18n } from "../i18n/context";
import { backupLabel, resourcesLabel } from "../lib/proxmox";
import { formatBytes } from "../lib/units";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, KeyValue } from "./Card";
import { MachineKind } from "./MachineKind";

// L'identité fixe d'une machine, à droite de sa fiche : où elle tourne (type,
// hôte, grappe), son adresse, son système, et ce que son hôte lui alloue. C'est
// ce qui disait « Sur l'hôte Proxmox » et « Proxmox », fondu en un seul bloc.
export function MachineIdentity({ machine, all }: { machine: Machine; all: Machine[] }) {
  const { t, language } = useI18n();
  const now = useNow();
  const host = machine.host;
  const hosted = machine.proxmox.hosted;
  // La grappe est celle de l'hôte qui la porte, ou la sienne s'il est lui-même un hôte.
  const hostMachine = host === null ? undefined : all.find((other) => other.id === host.machine_id);
  const cluster = machine.proxmox.cluster ?? hostMachine?.proxmox.cluster ?? null;
  const node = host?.node ?? machine.proxmox.node;
  return (
    <Card>
      <CardHeader
        title={t("machine.identity_title")}
        aside={
          <Button small variant="ghost" icon="settings" to={`/machines/${machine.id}/emplacement`}>
            {t("machine.identity_correct")}
          </Button>
        }
      />
      <CardBody gap={10}>
        <KeyValue
          label={t("machine.identity_kind")}
          value={
            <>
              <MachineKind machine={machine} />
              {machine.kind_override !== "" && <span className="secondary"> {t("machine.identity_corrected")}</span>}
            </>
          }
        />
        <KeyValue
          label={t("machine.identity_host")}
          value={
            host === null ? (
              <span className="secondary">{t("machine.identity_host_none")}</span>
            ) : (
              <>
                <Link to={`/machines/${host.machine_id}`}>{host.machine_name === "" ? host.machine_id : host.machine_name}</Link>
                {host.source === "override" && <span className="secondary"> {t("machine.identity_host_override")}</span>}
              </>
            )
          }
        />
        {cluster !== null && <KeyValue label={t("machine.identity_cluster")} value={cluster.name} />}
        {node !== "" && <KeyValue label={t("machine.identity_node")} value={node} mono />}
        <KeyValue label={t("machine.identity_address")} value={machine.address === "" ? "—" : machine.address} mono={machine.address !== ""} />
        <KeyValue label={t("machine.identity_os")} value={`${machine.os} · ${machine.arch}`} />
        <KeyValue label={t("machine.identity_agent")} value={machine.agent?.version ?? "—"} mono />
        {hosted !== null && (
          <>
            <KeyValue label={t("machine.identity_guest")} value={`${hosted.vmid} · ${hosted.name}`} mono />
            <KeyValue label={t("machine.identity_allocation")} value={`${resourcesLabel(t, language, hosted.cpus, hosted.max_memory)} · ${formatBytes(t, language, hosted.max_disk)}`} />
            <KeyValue label={t("machine.proxmox_col_backup")} value={backupLabel(t, language, { at: hosted.last_backup_at, bytes: hosted.last_backup_bytes, unknown: hosted.backup_unknown }, now)} />
          </>
        )}
        {machine.proxmox.declared && (
          <KeyValue
            label={t("machine.identity_hypervisor")}
            value={
              <Link to={`/machines/${machine.id}/hyperviseur`}>{t(machine.proxmox.guests === 1 ? "machine.identity_hypervisor_guests_one" : "machine.identity_hypervisor_guests", machine.proxmox.guests)}</Link>
            }
          />
        )}
      </CardBody>
    </Card>
  );
}
