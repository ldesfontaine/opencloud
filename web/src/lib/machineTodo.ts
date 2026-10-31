import type { Machine } from "../api/machines";
import { agentBehindServer } from "./agentUpdate";
import { isArchived } from "./machines";
import { detectedProxmox } from "./proxmox";

// Un fait de la fiche qui demande un geste de l'opérateur. Chacun a son bouton ;
// la liste est vide quand tout va, et le bloc « À faire » n'est alors pas là.
export type MachineTodo =
  | { kind: "agent_missing" }
  | { kind: "agent_behind"; serverVersion: string }
  | { kind: "rights_unavailable"; count: number }
  | { kind: "support_ended" }
  | { kind: "kind_unknown" }
  | { kind: "proxmox_to_declare"; version: string }
  | { kind: "proxmox_read_failed"; key: string };

// Ce que la fiche signale, dans l'ordre où l'opérateur doit s'en occuper : sans
// agent d'abord (rien d'autre ne se lit), puis ce que l'agent ne peut pas faire.
// Une machine archivée n'a rien à faire : son agent est coupé exprès.
export function machineTodos(machine: Machine, serverVersion: string): MachineTodo[] {
  if (isArchived(machine)) {
    return [];
  }
  const agent = machine.agent;
  if (agent === null) {
    return [{ kind: "agent_missing" }];
  }
  const todos: MachineTodo[] = [];
  if (agentBehindServer(agent.version, serverVersion)) {
    todos.push({ kind: "agent_behind", serverVersion });
  }
  const unavailable = agent.reported_at === null ? 0 : agent.capabilities.filter((capability) => capability.unavailable !== "").length;
  if (unavailable > 0) {
    todos.push({ kind: "rights_unavailable", count: unavailable });
  }
  if (machine.lifecycle?.status === "eol") {
    todos.push({ kind: "support_ended" });
  }
  if (machine.kind === "unknown" && machine.kind_override === "") {
    todos.push({ kind: "kind_unknown" });
  }
  todos.push(...proxmoxTodos(machine));
  return todos;
}

// Un hôte Proxmox que l'agent a reconnu (ou que le rôle annonce) sans que
// l'opérateur l'ait déclaré, ou dont le dernier relevé a échoué. Une VM d'un
// hôte déclaré n'est pas un hyperviseur : on ne le lui propose pas.
function proxmoxTodos(machine: Machine): MachineTodo[] {
  const summary = machine.proxmox;
  if (summary.declared) {
    return summary.last_error === "" ? [] : [{ kind: "proxmox_read_failed", key: summary.last_error }];
  }
  const detected = detectedProxmox(machine);
  if (summary.hosted !== null || (detected === null && machine.role !== "proxmox")) {
    return [];
  }
  return [{ kind: "proxmox_to_declare", version: detected?.version ?? "" }];
}
