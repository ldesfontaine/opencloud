import { useEffect, useRef, useState, type FormEvent, type MouseEvent, type ReactNode } from "react";

import type { ApiError } from "../api/client";
import { useT } from "../i18n/context";
import {
  canJumpFromBar,
  forwardTarget,
  furthestReached,
  initialPosition,
  isSummary,
  moveTo,
  previousPosition,
  stepMark,
  summaryPosition,
  type WizardMode,
} from "../lib/wizard";
import { Button } from "./Button";
import { Card, KeyValue } from "./Card";
import { Failure } from "./Failure";
import { Icon, type IconName } from "./Icon";
import "./Wizard.scss";

// Une ligne du récapitulatif : ce que l'opérateur a saisi, sous son nom.
export interface SummaryLine {
  label: string;
  value: ReactNode;
  mono?: boolean;
}

// Une étape : ses champs, sa validation (la clé du refus, ou null), et ce
// qu'elle dit au récapitulatif. Les valeurs vivent chez l'appelant
// (useForm) : une étape quittée est démontée sans rien perdre.
export interface WizardStep {
  id: string;
  title: string;
  render: () => ReactNode;
  validate?: () => string | null;
  summary: () => SummaryLine[];
}

// L'assistant : une étape à l'écran, Précédent et Suivant, puis le
// récapitulatif et l'action finale. Entrée dans un champ vaut Suivant ;
// seule l'action du récapitulatif écrit. L'erreur du serveur (error) se dit
// sur le récapitulatif.
export function Wizard({
  steps,
  mode,
  onSubmit,
  submitLabel,
  submitIcon,
  busy,
  error,
  cancelLabel,
  cancelTo,
  onCancel,
}: {
  steps: WizardStep[];
  mode: WizardMode;
  onSubmit: () => void;
  submitLabel: string;
  submitIcon: IconName;
  busy: boolean;
  error: ApiError | string | null;
  cancelLabel: string;
  cancelTo?: string;
  onCancel?: () => void;
}) {
  const t = useT();
  const stepCount = steps.length;
  const [position, setPosition] = useState(() => initialPosition(mode, stepCount));
  const [reached, setReached] = useState(() => (mode === "edit" ? summaryPosition(stepCount) : position));
  const [stepError, setStepError] = useState<string | null>(null);
  const title = useRef<HTMLHeadingElement>(null);
  const focusedPosition = useRef(position);

  // Le focus suit l'étape : le lecteur d'écran annonce son titre. Pas à
  // l'arrivée, où il volerait le focus posé par la page.
  useEffect(() => {
    if (focusedPosition.current === position) {
      return;
    }
    focusedPosition.current = position;
    title.current?.focus();
  }, [position]);

  const validate = (index: number) => steps[index]?.validate?.() ?? null;

  const go = (target: number) => {
    const move = moveTo(position, target, validate);
    setStepError(move.error);
    setPosition(move.position);
    setReached((current) => furthestReached(current, move.position));
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (isSummary(position, stepCount)) {
      onSubmit();
      return;
    }
    go(forwardTarget(position, stepCount, reached));
  };

  // Suivant de la dernière étape et l'action finale sont à la même place :
  // le second clic d'un double-clic n'écrit pas.
  const ignoreRepeatedClick = (event: MouseEvent) => {
    if (event.detail > 1) {
      event.preventDefault();
    }
  };

  const onSummary = isSummary(position, stepCount);
  const shownError = onSummary ? error : stepError;
  const titles = [...steps.map((step) => step.title), t("wizard.summary")];
  const forwardLabel = reached >= summaryPosition(stepCount) ? t("wizard.back_to_summary") : t("wizard.next");

  return (
    <div className="wizard">
      <WizardBar titles={titles} position={position} reached={reached} canJump={canJumpFromBar(mode)} onJump={go} />
      <Card>
        <form onSubmit={submit}>
          <div className="card-h">
            <h2 className="card-t wizard-title" ref={title} tabIndex={-1}>
              {titles[position]}
            </h2>
          </div>
          <div className="card-b">
            {onSummary ? <WizardSummary steps={steps} onEdit={go} /> : steps[position]?.render()}
            {/* Annoncée dès qu'elle paraît : le focus reste sur le champ ou le bouton. */}
            {shownError !== null && (
              <div role="alert">
                <Failure error={shownError} />
              </div>
            )}
            <div className="cluster">
              {position > 0 && <Button onClick={() => go(previousPosition(position))}>{t("wizard.previous")}</Button>}
              {onSummary ? (
                <Button type="submit" variant="primary" icon={submitIcon} disabled={busy} onClick={ignoreRepeatedClick}>
                  {submitLabel}
                </Button>
              ) : (
                <Button type="submit" variant="primary">
                  {forwardLabel}
                </Button>
              )}
              {cancelTo !== undefined && (
                <Button variant="ghost" to={cancelTo}>
                  {cancelLabel}
                </Button>
              )}
              {cancelTo === undefined && onCancel !== undefined && (
                <Button variant="ghost" onClick={onCancel}>
                  {cancelLabel}
                </Button>
              )}
            </div>
          </div>
        </form>
      </Card>
    </div>
  );
}

// La barre d'étapes : numéro et nom, la courante marquée. En modification,
// chaque étape y mène. Sous 860 px, elle se réduit à « Étape 2 sur 4 » : le
// nom est juste dessous, en tête de carte.
function WizardBar({
  titles,
  position,
  reached,
  canJump,
  onJump,
}: {
  titles: string[];
  position: number;
  reached: number;
  canJump: boolean;
  onJump: (target: number) => void;
}) {
  const t = useT();
  return (
    <nav className="wizard-bar" aria-label={t("wizard.steps_label")}>
      <ol className="wizard-steps">
        {titles.map((stepTitle, index) => {
          const mark = stepMark(index, position, reached);
          const content = (
            <>
              <span className="wizard-mark">{mark === "done" ? <Icon name="check" /> : index + 1}</span>
              <span className="wizard-name">{stepTitle}</span>
            </>
          );
          return (
            <li key={index} className={`wizard-step wizard-step-${mark}`} aria-current={mark === "current" ? "step" : undefined}>
              {canJump && mark !== "current" ? (
                <button type="button" className="wizard-jump" onClick={() => onJump(index)}>
                  {content}
                </button>
              ) : (
                <span className="wizard-jump">{content}</span>
              )}
            </li>
          );
        })}
      </ol>
      <p className="wizard-compact">{t("wizard.step_of", position + 1, titles.length)}</p>
    </nav>
  );
}

// Le récapitulatif : chaque étape sous son titre, ce qui a été saisi, et
// « Modifier » qui y ramène.
function WizardSummary({ steps, onEdit }: { steps: WizardStep[]; onEdit: (target: number) => void }) {
  const t = useT();
  return (
    <div className="wizard-summary">
      {steps.map((step, index) => (
        <section key={step.id} className="wizard-summary-step">
          <div className="wizard-summary-head">
            <h3 className="side-label inline-label">{step.title}</h3>
            <Button small variant="ghost" onClick={() => onEdit(index)} aria-label={t("wizard.edit_step", step.title)}>
              {t("wizard.edit")}
            </Button>
          </div>
          {step.summary().map((line) => (
            <KeyValue key={line.label} label={line.label} value={line.value} mono={line.mono ?? false} />
          ))}
        </section>
      ))}
    </div>
  );
}
