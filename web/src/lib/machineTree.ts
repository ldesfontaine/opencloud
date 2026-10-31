import type { Machine } from "../api/machines";
import type { ProxmoxCluster, ProxmoxMember } from "../api/proxmox";
import { isArchived } from "./machines";

// L'arbre des machines : un hôte porte ses invités, une grappe ses nœuds. Il se
// construit ici, en fonction pure, de la liste des machines : le serveur ne
// sert pas de seconde lecture des mêmes lignes.
//
// Un invité que l'hôte voit et qu'aucune machine enrôlée ne porte reste dans
// l'arbre, « sans agent » : c'est ce qui manque à la supervision. Le niveau
// « grappe » n'apparaît qu'à partir de deux nœuds : un seul n'apprend rien.

export type TreeRow =
  | { type: "machine"; key: string; depth: number; machine: Machine }
  | { type: "guest"; key: string; depth: number; guest: ProxmoxMember }
  | { type: "cluster"; key: string; depth: number; cluster: ProxmoxCluster; nodes: number }
  | { type: "node"; key: string; depth: number; name: string };

interface Branch {
  row: TreeRow;
  children: Branch[];
  // Une machine de la branche est hors ligne : la branche passe en tête.
  needsAttention: boolean;
}

// Le nœud où l'hôte place l'invité, quand il le dit : vide pour un hôte désigné
// à la main, qui ne dit rien d'un nœud.
function nodeOf(machine: Machine): string {
  return machine.host?.source === "guest" ? machine.host.node : "";
}

function byName(left: Machine, right: Machine): number {
  return left.name.localeCompare(right.name);
}

function isMissing(machine: Machine): boolean {
  return !isArchived(machine) && !machine.online;
}

export function buildMachineTree(machines: Machine[]): TreeRow[] {
  return new TreeBuilder(machines).build();
}

class TreeBuilder {
  private readonly byID = new Map<string, Machine>();
  private readonly childrenOf = new Map<string, Machine[]>();
  private readonly placed = new Set<string>();

  constructor(private readonly machines: Machine[]) {
    for (const machine of machines) {
      this.byID.set(machine.id, machine);
    }
    for (const machine of [...machines].sort(byName)) {
      const parent = this.parentOf(machine);
      if (parent !== null) {
        this.childrenOf.set(parent, [...(this.childrenOf.get(parent) ?? []), machine]);
      }
    }
  }

  build(): TreeRow[] {
    const roots: Branch[] = [...this.clusterBranches()];
    for (const machine of [...this.machines].sort(byName)) {
      if (!this.placed.has(machine.id) && this.parentOf(machine) === null) {
        roots.push(this.machineBranch(machine));
      }
    }
    // Ce qui demande l'opérateur d'abord, puis l'ordre des noms (le tri est stable).
    roots.sort((left, right) => Number(right.needsAttention) - Number(left.needsAttention));
    return roots.flatMap((root) => flatten(root, 0));
  }

  // Le parent d'une machine : son hôte, s'il est dans la liste. Une boucle
  // (A sur B, B sur A) n'a pas de parent : ses machines se montrent à la racine
  // plutôt que de disparaître.
  private parentOf(machine: Machine): string | null {
    const hostID = machine.host?.machine_id;
    if (hostID === undefined || !this.byID.has(hostID) || this.isCarriedBy(hostID, machine.id)) {
      return null;
    }
    return hostID;
  }

  // L'hôte est-il, de proche en proche, porté par la machine ? Alors le désigner
  // comme parent ferait une boucle.
  private isCarriedBy(hostID: string, machineID: string): boolean {
    const seen = new Set<string>();
    let currentID: string | undefined = hostID;
    while (currentID !== undefined && !seen.has(currentID)) {
      if (currentID === machineID) {
        return true;
      }
      seen.add(currentID);
      currentID = this.byID.get(currentID)?.host?.machine_id;
    }
    return false;
  }

  // La branche d'une machine : elle, les machines qu'elle porte, et, pour un
  // hôte déclaré, ses invités sans agent (ceux qu'on lui donne, sinon tous les siens).
  private machineBranch(machine: Machine, scope: Scope = {}): Branch {
    this.placed.add(machine.id);
    const carried = (this.childrenOf.get(machine.id) ?? []).filter(scope.isCarried ?? (() => true));
    const children = [...carried.map((child) => this.machineBranch(child)), ...this.guestBranches(scope.guests ?? machine.proxmox.guest_list)];
    return withChildren({ type: "machine", key: machine.id, depth: 0, machine }, children, isMissing(machine));
  }

  // Les invités de l'hôte qu'aucune machine ne porte, par numéro ; les modèles
  // ne sont pas des machines.
  private guestBranches(members: ProxmoxMember[]): Branch[] {
    return members
      .filter((member) => !member.template && member.linked_machine_id === null)
      .sort((left, right) => left.vmid - right.vmid)
      .map((guest) => withChildren({ type: "guest", key: `guest-${guest.node}-${guest.vmid}`, depth: 0, guest }, [], false));
  }

  private clusterBranches(): Branch[] {
    const hosts = this.machines.filter((machine) => machine.proxmox.declared && machine.proxmox.cluster !== null);
    const clusters = new Map<string, Machine[]>();
    for (const host of hosts) {
      const id = host.proxmox.cluster?.id ?? "";
      clusters.set(id, [...(clusters.get(id) ?? []), host]);
    }
    const branches: Branch[] = [];
    for (const members of clusters.values()) {
      const branch = this.clusterBranch(members.sort(byName));
      if (branch !== null) {
        branches.push(branch);
      }
    }
    return branches.sort((left, right) => clusterName(left).localeCompare(clusterName(right)));
  }

  // La grappe, ses nœuds, et sous chacun ses machines et ses invités sans agent.
  // Moins de deux nœuds : pas de niveau « grappe », l'hôte garde sa branche.
  private clusterBranch(members: Machine[]): Branch | null {
    const cluster = members[0]?.proxmox.cluster;
    const names = nodeNames(members);
    if (cluster === null || cluster === undefined || names.length < 2) {
      return null;
    }
    const nodes = names.map((name) => this.nodeBranch(name, members));
    return withChildren({ type: "cluster", key: `cluster-${cluster.id}`, depth: 0, cluster, nodes: names.length }, nodes, false);
  }

  // Un nœud de la grappe : sa machine hôte si elle est enrôlée, sinon une ligne
  // « nœud » ; dessous, ce qu'il porte. Un invité que plusieurs hôtes de la
  // grappe listent ne se montre qu'une fois.
  private nodeBranch(name: string, members: Machine[]): Branch {
    const owner = members.find((member) => member.proxmox.node === name);
    const guests = dedupeByVMID(members.flatMap((member) => member.proxmox.guest_list).filter((guest) => guest.node === name));
    const hosted = this.hostedOnNode(name, members, owner).map((child) => this.machineBranch(child));
    if (owner === undefined) {
      return withChildren({ type: "node", key: `node-${name}`, depth: 0, name }, [...hosted, ...this.guestBranches(guests)], false);
    }
    // Les machines de l'hôte posées sur un autre nœud sont à leur nœud.
    const own = this.machineBranch(owner, { guests, isCarried: (child) => nodeOf(child) === "" || nodeOf(child) === name });
    return withChildren(own.row, [...own.children, ...hosted], isMissing(owner));
  }

  // Les machines que les autres hôtes de la grappe portent sur ce nœud.
  private hostedOnNode(name: string, members: Machine[], owner: Machine | undefined): Machine[] {
    return members
      .filter((member) => member !== owner)
      .flatMap((member) => this.childrenOf.get(member.id) ?? [])
      .filter((child) => nodeOf(child) === name)
      .sort(byName);
  }
}

// Ce qu'une branche d'hôte reprend quand elle n'est qu'un nœud de la grappe :
// seulement les invités de ce nœud et les machines qui y sont posées.
interface Scope {
  guests?: ProxmoxMember[];
  isCarried?: (child: Machine) => boolean;
}

function nodeNames(members: Machine[]): string[] {
  const names = new Set<string>();
  for (const member of members) {
    if (member.proxmox.node !== "") {
      names.add(member.proxmox.node);
    }
    for (const guest of member.proxmox.guest_list) {
      if (guest.node !== "") {
        names.add(guest.node);
      }
    }
  }
  return [...names].sort((left, right) => left.localeCompare(right));
}

function dedupeByVMID(members: ProxmoxMember[]): ProxmoxMember[] {
  const seen = new Set<number>();
  return members.filter((member) => !seen.has(member.vmid) && !!seen.add(member.vmid));
}

function clusterName(branch: Branch): string {
  return branch.row.type === "cluster" ? branch.row.cluster.name : "";
}

function withChildren(row: TreeRow, children: Branch[], isItselfMissing: boolean): Branch {
  return { row, children, needsAttention: isItselfMissing || children.some((child) => child.needsAttention) };
}

function flatten(branch: Branch, depth: number): TreeRow[] {
  return [{ ...branch.row, depth }, ...branch.children.flatMap((child) => flatten(child, depth + 1))];
}

// Ce que la liste dit en tête : combien de machines, combien répondent, celles
// qui manquent, les invités que nul agent ne suit, les types qu'on ne sait pas.
export interface TreeSummary {
  total: number;
  online: number;
  missing: Machine[];
  guestsWithoutAgent: number;
  unknownKinds: number;
}

export function summarizeMachines(machines: Machine[], rows: TreeRow[]): TreeSummary {
  const live = machines.filter((machine) => !isArchived(machine));
  return {
    total: live.length,
    online: live.filter((machine) => machine.online).length,
    missing: live.filter(isMissing),
    guestsWithoutAgent: rows.filter((row) => row.type === "guest").length,
    unknownKinds: live.filter((machine) => machine.kind === "unknown").length,
  };
}
