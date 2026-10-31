import type { Machine } from "../api/machines";
import { useI18n, type Translate } from "../i18n/context";
import { machineTodos, type MachineTodo } from "../lib/machineTodo";
import { readErrorLabel } from "../lib/proxmox";
import { Button } from "./Button";
import { Card, CardBody, CardHeader } from "./Card";
import "./TodoCard.scss";

// Le bloc « À faire » de la fiche : les faits qui demandent un geste, chacun
// avec son bouton. Sans rien à faire, il n'est pas là.
export function MachineTodoCard({ machine }: { machine: Machine }) {
  const { t, version } = useI18n();
  const todos = machineTodos(machine, version);
  if (todos.length === 0) {
    return null;
  }
  return (
    <Card>
      <CardHeader title={t("machine.todo_title")} />
      <CardBody gap={8}>
        <ul className="todo-list">
          {todos.map((todo) => (
            <li key={todo.kind} className="todo-item">
              <span className="todo-text">{todoText(t, todo)}</span>
              <Button small to={todoTarget(machine.id, todo)}>
                {t(`machine.todo_${todo.kind}_action`)}
              </Button>
            </li>
          ))}
        </ul>
      </CardBody>
    </Card>
  );
}

function todoText(t: Translate, todo: MachineTodo): string {
  switch (todo.kind) {
    case "agent_behind":
      return t("machine.todo_agent_behind", todo.serverVersion);
    case "rights_unavailable":
      return todo.count === 1 ? t("machine.todo_rights_unavailable_one") : t("machine.todo_rights_unavailable", todo.count);
    case "proxmox_to_declare":
      return todo.version === "" ? t("machine.todo_proxmox_to_declare_unversioned") : t("machine.todo_proxmox_to_declare", todo.version);
    case "proxmox_read_failed":
      return t("machine.todo_proxmox_read_failed", readErrorLabel(t, todo.key));
    default:
      return t(`machine.todo_${todo.kind}`);
  }
}

// Là où le geste se fait : une fenêtre à son adresse, un onglet, ou l'ancre
// du détail du support sur la page même.
function todoTarget(machineID: string, todo: MachineTodo): string {
  const base = `/machines/${machineID}`;
  switch (todo.kind) {
    case "agent_missing":
      return `${base}/reenroller`;
    case "agent_behind":
    case "rights_unavailable":
      return `${base}/agent`;
    case "support_ended":
      return `${base}#support`;
    case "kind_unknown":
      return `${base}/emplacement`;
    case "proxmox_to_declare":
      return `${base}/proxmox`;
    case "proxmox_read_failed":
      return `${base}/hyperviseur`;
  }
}
