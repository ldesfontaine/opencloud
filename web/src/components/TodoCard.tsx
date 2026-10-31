import { useT } from "../i18n/context";
import type { Todo } from "../lib/modules";
import { Button } from "./Button";
import { Card, CardBody, CardHeader } from "./Card";
import "./TodoCard.scss";

// La carte « à configurer » de la vue d'ensemble : les modules qui attendent
// un premier réglage, chacun avec son bouton. Sans rien à faire, elle n'est
// pas là.
export function TodoCard({ todos }: { todos: Todo[] }) {
  const t = useT();
  if (todos.length === 0) {
    return null;
  }
  return (
    <Card>
      <CardHeader title={t("overview.todo_title")} />
      <CardBody gap={8}>
        <p className="secondary">{t("overview.todo_text")}</p>
        <ul className="todo-list">
          {todos.map((todo) => (
            <li key={`${todo.module}-${todo.kind === "hypervisor" ? todo.machineName : ""}`} className="todo-item">
              <span className="todo-text">
                <b>{t(`module.${todo.module}`)}</b>
                <span className="secondary">{todo.kind === "hypervisor" ? t("overview.todo_hypervisor", todo.machineName) : t(`module.${todo.module}_text`)}</span>
              </span>
              <Button small to={todo.href}>
                {t("overview.todo_configure")}
              </Button>
            </li>
          ))}
        </ul>
      </CardBody>
    </Card>
  );
}
