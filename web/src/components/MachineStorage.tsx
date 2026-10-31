import type { ReactNode } from "react";
import { Link } from "react-router";

import type { Machine } from "../api/machines";
import type { Current } from "../api/resources";
import { useMachineStorage, type DiskMedia, type PhysicalDisk, type PoolState, type StoragePool } from "../api/storage";
import { useI18n } from "../i18n/context";
import type { Tone } from "../lib/tone";
import { formatBytes } from "../lib/units";
import { Card, CardBody, CardHeader, Note } from "./Card";
import { Failure } from "./Failure";
import { Pill } from "./Pill";
import { DiskVolumes } from "./Resources";
import { Subject, Table } from "./Table";
import "./MachineStorage.scss";

const poolTones: Record<PoolState, Tone> = { ok: "ok", degraded: "warn", failed: "danger", unknown: "neutral" };

// Le stockage d'une machine : ses volumes montés (la jauge de chacun), les
// disques physiques que le noyau expose, les pools qui les regroupent, et, pour
// un hôte Proxmox, ce que ses invités s'y font allouer. Les disques et les pools
// viennent de l'agent, sans droit ; aucun relevé SMART (il viendra comme un droit
// à part).
export function MachineStorage({ machine, current }: { machine: Machine; current: Current | null }) {
  const { t } = useI18n();
  const storage = useMachineStorage(machine.id);
  const volumes = current?.available ? (current.sample?.disks ?? []) : [];
  const disks = storage.data?.disks ?? [];
  const pools = storage.data?.pools ?? [];
  const allocated = machine.proxmox.guest_list.filter((guest) => !guest.template && guest.max_disk > 0).sort((left, right) => right.max_disk - left.max_disk);
  return (
    <Card>
      <CardHeader title={t("storage.title")} />
      <CardBody gap={16}>
        {storage.error && <Failure error={storage.error} />}
        {volumes.length > 0 && (
          <Section title={t("storage.volumes")}>
            <DiskVolumes disks={volumes} />
          </Section>
        )}
        {storage.data && disks.length === 0 && pools.length === 0 && volumes.length === 0 && <Note tone="warn">{t("storage.unreported")}</Note>}
        {disks.length > 0 && (
          <Section title={t("storage.disks")}>
            <DisksTable disks={disks} />
          </Section>
        )}
        {pools.length > 0 && (
          <Section title={t("storage.pools")}>
            <PoolsTable pools={pools} />
          </Section>
        )}
        {allocated.length > 0 && (
          <Section title={t("storage.allocated")}>
            <AllocatedTable guests={allocated} />
          </Section>
        )}
        {storage.data && <p className="secondary">{t("storage.no_smart")}</p>}
      </CardBody>
    </Card>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="storage-section">
      <h3 className="side-label">{title}</h3>
      {children}
    </section>
  );
}

function mediaTone(media: DiskMedia): Tone {
  return media === "unknown" || media === "virtual" ? "neutral" : "accent";
}

function DisksTable({ disks }: { disks: PhysicalDisk[] }) {
  const { t, language } = useI18n();
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("storage.col_disk")}</th>
          <th className="wide-only">{t("storage.col_model")}</th>
          <th>{t("storage.col_media")}</th>
          <th className="th-end">{t("storage.col_size")}</th>
        </tr>
      </thead>
      <tbody>
        {disks.map((disk) => (
          <tr key={disk.name}>
            <td>
              <Subject icon="disk">
                <span className="mono">{disk.name}</span>
                {disk.removable && <span className="secondary"> {t("storage.removable")}</span>}
              </Subject>
            </td>
            <td className="wide-only secondary">{disk.model === "" ? "—" : disk.model}</td>
            <td>
              <Pill tone={mediaTone(disk.media)} dot={false} small>
                {t(`storage.media_${disk.media}`)}
              </Pill>
            </td>
            <td className="num td-end">{formatBytes(t, language, disk.size)}</td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
}

function PoolsTable({ pools }: { pools: StoragePool[] }) {
  const { t, language } = useI18n();
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("storage.col_pool")}</th>
          <th>{t("storage.col_state")}</th>
          <th className="wide-only">{t("storage.col_members")}</th>
          <th className="th-end">{t("storage.col_size")}</th>
        </tr>
      </thead>
      <tbody>
        {pools.map((pool) => (
          <tr key={pool.name}>
            <td>
              <Subject icon="database">
                <span className="mono">{pool.name}</span>
                <span className="secondary"> {pool.level === "" ? t(`storage.pool_${pool.kind}`) : `${t(`storage.pool_${pool.kind}`)} ${pool.level}`}</span>
              </Subject>
            </td>
            <td>
              <Pill tone={poolTones[pool.state]}>{t(`storage.state_${pool.state}`)}</Pill>
            </td>
            <td className="wide-only mono">{pool.members.length === 0 ? "—" : pool.members.join(", ")}</td>
            <td className="num td-end">{pool.size === 0 ? "—" : formatBytes(t, language, pool.size)}</td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
}

// Le disque que chaque invité se fait allouer : une promesse de l'hôte, pas ce
// qu'il consomme. Un invité enrôlé mène à sa fiche.
function AllocatedTable({ guests }: { guests: Machine["proxmox"]["guest_list"] }) {
  const { t, language } = useI18n();
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("storage.col_guest")}</th>
          <th className="th-end">{t("storage.col_allocated")}</th>
        </tr>
      </thead>
      <tbody>
        {guests.map((guest) => (
          <tr key={guest.vmid}>
            <td>
              <Subject icon="server">
                {guest.linked_machine_id === null ? guest.name : <Link to={`/machines/${guest.linked_machine_id}`}>{guest.name}</Link>} <span className="secondary mono">{guest.vmid}</span>
              </Subject>
            </td>
            <td className="num td-end">{formatBytes(t, language, guest.max_disk)}</td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
}
