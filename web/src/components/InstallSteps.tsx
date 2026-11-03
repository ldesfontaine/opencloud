import type { InstallStep } from "../api/machines";
import { useT } from "../i18n/context";
import { Note } from "./Card";
import { Checklist } from "./Checklist";
import { TokenBox } from "./Copy";

// Les étapes d'installation d'un agent, dans l'ordre où on les colle sur la
// machine : une case, un titre, une ligne, un seul bloc à copier. Rien ne
// renvoie à la documentation. Les cases aident à suivre où l'on en est ;
// l'appelant les tient pour qu'une étape quittée n'oublie rien (useChecklist).
// Le jeton n'est que dans le bloc « enroll » : il ne s'affiche qu'une fois.
export function InstallSteps({
  steps,
  produces,
  done,
  onToggle,
}: {
  steps: InstallStep[];
  // Le droit de produire des sauvegardes vaut root : la préparation le dit.
  produces: boolean;
  done: readonly string[];
  onToggle: (id: string, checked: boolean) => void;
}) {
  const t = useT();
  const items = steps.map((step) => ({
    id: step.id,
    title: t(`machine.install_${step.id}_title`),
    content: (
      <>
        <p className="secondary">{t(`machine.install_${step.id}_text`)}</p>
        <TokenBox value={step.command} lines />
        {step.id === "prepare" && <Note tone="warn">{t("machine.prepare_docker_note")}</Note>}
        {step.id === "prepare" && produces && <Note tone="warn">{t("machine.capability_produce_warning")}</Note>}
      </>
    ),
  }));
  return <Checklist items={items} done={done} onToggle={onToggle} />;
}
