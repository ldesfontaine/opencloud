import { Link } from "react-router";

import type { Current } from "../../api/resources";
import { Button } from "../../components/Button";
import { Icon } from "../../components/Icon";
import { MachineKindPill, MachineStatePill, Pill } from "../../components/Pill";
import { MachineMeters } from "../../components/Resources";
import { Subject } from "../../components/Table";
import { useI18n } from "../../i18n/context";
import type { TreeRow } from "../../lib/machineTree";
import { machineSeen } from "../../lib/machines";
import { guestStatusLabel, resourcesLabel } from "../../lib/proxmox";
import "./MachineTree.scss";

// Le nombre de colonnes du tableau : une ligne de groupe les couvre toutes.
const columns = 7;

// Les lignes du tableau des machines : un hôte, ses invités en retrait (machines
// enrôlées, puis invités que nul agent ne suit), une grappe et ses nœuds.
export function MachineTreeRows({ rows, resources, now }: { rows: TreeRow[]; resources: ReadonlyMap<string, Current>; now: number }) {
  return (
    <>
      {rows.map((row) => {
        switch (row.type) {
          case "machine":
            return <MachineRow key={row.key} row={row} current={resources.get(row.machine.id)} now={now} />;
          case "guest":
            return <GuestRow key={row.key} row={row} />;
          case "cluster":
            return <GroupRow key={row.key} depth={row.depth} icon="network" label={row.cluster.name} countKey="machines.cluster_nodes" count={row.nodes} />;
          case "node":
            return <GroupRow key={row.key} depth={row.depth} icon="server" label={row.name} text="machines.node_without_agent" />;
        }
      })}
    </>
  );
}

// Le retrait d'une ligne : 20 px par niveau, posé en variable CSS pour que la
// feuille de style tienne la mesure.
function indent(depth: number) {
  return { "--depth": depth } as React.CSSProperties;
}

function MachineRow({ row, current, now }: { row: Extract<TreeRow, { type: "machine" }>; current: Current | undefined; now: number }) {
  const { t } = useI18n();
  const machine = row.machine;
  return (
    <tr>
      <td>
        <span className="tree-name" style={indent(row.depth)}>
          <Link className="svc" to={`/machines/${machine.id}`}>
            <Subject icon="server">{machine.name}</Subject>
          </Link>
          <MachineKindPill kind={machine.kind} />
        </span>
      </td>
      <td className="num wide-only">{machine.address}</td>
      <td className="wide-only tree-meters">
        <MachineMeters current={current} />
      </td>
      <td>
        <MachineStatePill machine={machine} />
      </td>
      <td className="secondary nowrap">{machineSeen(t, machine, now)}</td>
      <td className="num wide-only">{machine.agent?.version ?? "—"}</td>
      <td className="td-end wide-only">
        <Button variant="ghost" small to={`/machines/${machine.id}`}>
          {t("machines.open")}
        </Button>
      </td>
    </tr>
  );
}

// Un invité que l'hôte voit et qu'aucun agent ne suit : grisé, avec de quoi
// l'enrôler. Ce qui lui est alloué est ce que l'hôte annonce, pas ce qu'il consomme.
function GuestRow({ row }: { row: Extract<TreeRow, { type: "guest" }> }) {
  const { t, language } = useI18n();
  const guest = row.guest;
  return (
    <tr className="tree-guest">
      <td>
        <span className="tree-name" style={indent(row.depth)}>
          <Subject icon="server">
            {guest.name === "" ? guest.vmid : guest.name} <span className="secondary mono">{guest.vmid}</span>
          </Subject>
          <Pill tone="neutral" dot={false} small>
            {t(guest.kind === "lxc" ? "machine.kind_lxc" : "machine.kind_vm")}
          </Pill>
        </span>
      </td>
      <td className="num wide-only">—</td>
      <td className="wide-only secondary">{resourcesLabel(t, language, guest.cpus, guest.max_memory)}</td>
      <td>
        <Pill tone="neutral">{t("machines.guest_without_agent", guestStatusLabel(t, guest.status))}</Pill>
      </td>
      <td className="secondary nowrap">—</td>
      <td className="num wide-only">—</td>
      <td className="td-end wide-only">
        <Button variant="ghost" small to="/machines/nouvelle">
          {t("machines.enroll_guest")}
        </Button>
      </td>
    </tr>
  );
}

function GroupRow({ depth, icon, label, countKey, count, text }: { depth: number; icon: "network" | "server"; label: string; countKey?: string; count?: number; text?: string }) {
  const { t } = useI18n();
  return (
    <tr className="tree-group">
      <td colSpan={columns}>
        <span className="tree-name" style={indent(depth)}>
          <Icon name={icon} />
          <b>{label}</b>
          <span className="secondary">{countKey !== undefined && count !== undefined ? t(countKey, count) : text !== undefined ? t(text) : ""}</span>
        </span>
      </td>
    </tr>
  );
}
