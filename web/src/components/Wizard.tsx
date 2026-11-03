import { useEffect, useRef, useState, type FormEvent, type MouseEvent, type ReactNode } from "react";

import type { ApiError } from "../api/client";
import { useT } from "../i18n/context";
import {
  canJumpFromBar,
  forwardTarget,
  furthestReached,
  isOneScreen,
  isSummary,
  moveTo,
  previousPosition,
  startPosition,
  stepMark,
  summaryPosition,
  type WizardMode,
} from "../lib/wizard";
import { Button } from "./Button";
import { KeyValue } from "./Card";
import { Failure, type RightGesture } from "./Failure";
import { Icon, type IconName } from "./Icon";
import { ModalCancel } from "./Modal";
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
  // Vrai tant que l'étape ne peut pas mener plus loin (rien de choisissable,
  // l'étape dit pourquoi) : Suivant est grisé.
  blocked?: () => boolean;
  // Sans objet pour un assistant d'un seul écran : il n'a pas de récapitulatif.
  summary?: () => SummaryLine[];
}

interface WizardProps {
  steps: WizardStep[];
  mode: WizardMode;
  onSubmit: () => void;
  submitLabel: string;
  submitIcon: IconName;
  busy: boolean;
  error: ApiError | string | null;
  // Où mène un refus de droit de l'écriture finale : voir Failure.
  errorGesture?: RightGesture | undefined;
  // Ce qui suit le refus de l'écriture finale : le geste qui y remédie,
  // quand ce n'est pas un droit (un logiciel à installer).
  errorAside?: ReactNode;
  // L'identifiant de l'étape où s'ouvre une modification (un lien direct).
  startAt?: string | undefined;
}

// L'assistant. Ce qui tient en un écran n'est pas un assistant : une seule
// étape se rend en un formulaire, avec l'action finale (OneScreen). Les
// réglages dont les valeurs par défaut suffisent s'y replient dans un
// « Réglages » (SettingsFold), plutôt que de former une étape de plus.
export function Wizard(props: WizardProps) {
  return isOneScreen(props.steps.length) ? <OneScreen {...props} /> : <SteppedWizard {...props} />;
}

// Le second clic d'un double-clic sur l'action finale n'écrit pas : Suivant
// de la dernière étape et l'action finale sont à la même place.
function ignoreRepeatedClick(event: MouseEvent) {
  if (event.detail > 1) {
    event.preventDefault();
  }
}

// Un écran : les champs, l'action finale, « Annuler » (celui de la fenêtre).
// La validation de l'étape passe avant l'écriture ; l'erreur du serveur se
// dit au même endroit.
function OneScreen({ steps, onSubmit, submitLabel, submitIcon, busy, error, errorGesture }: WizardProps) {
  const [stepError, setStepError] = useState<string | null>(null);
  const step = steps[0];
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const problem = step?.validate?.() ?? null;
    setStepError(problem);
    if (problem === null) {
      onSubmit();
    }
  };
  const shownError = stepError ?? error;
  return (
    <div className="wizard">
      <form className="wizard-modal" onSubmit={submit} aria-label={step?.title}>
        {step?.render()}
        {shownError !== null && (
          <div role="alert">
            <Failure error={shownError} gesture={stepError === null ? errorGesture : undefined} />
          </div>
        )}
        <div className="cluster">
          <Button type="submit" variant="primary" icon={submitIcon} disabled={busy} onClick={ignoreRepeatedClick}>
            {submitLabel}
          </Button>
          <ModalCancel />
        </div>
      </form>
    </div>
  );
}

// L'assistant pas à pas : une étape à l'écran, Précédent et Suivant, puis le
// récapitulatif et l'action finale. Entrée dans un champ vaut Suivant ;
// seule l'action du récapitulatif écrit. L'erreur du serveur (error) se dit
// sur le récapitulatif.
// L'assistant est le contenu d'une fenêtre (Modal), qui fait le cadre :
// « Annuler » est celui de la fenêtre, qui demande avant d'abandonner une
// saisie.
function SteppedWizard({ steps, mode, onSubmit, submitLabel, submitIcon, busy, error, errorGesture, errorAside, startAt }: WizardProps) {
  const t = useT();
  const stepCount = steps.length;
  const [position, setPosition] = useState(() => startPosition(mode, steps.map((step) => step.id), startAt));
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

  const onSummary = isSummary(position, stepCount);
  const shownError = onSummary ? error : stepError;
  const titles = [...steps.map((step) => step.title), t("wizard.summary")];
  const forwardLabel = reached >= summaryPosition(stepCount) ? t("wizard.back_to_summary") : t("wizard.next");

  return (
    <div className="wizard">
      <WizardBar titles={titles} position={position} reached={reached} canJump={canJumpFromBar(mode)} onJump={go} />
      <form className="wizard-modal" onSubmit={submit}>
        <h3 className="card-t wizard-title" ref={title} tabIndex={-1}>
          {titles[position]}
        </h3>
        {onSummary ? <WizardSummary steps={steps} onEdit={go} /> : steps[position]?.render()}
        {/* Annoncée dès qu'elle paraît : le focus reste sur le champ ou le bouton. */}
        {shownError !== null && (
          <div role="alert">
            <Failure error={shownError} gesture={onSummary ? errorGesture : undefined} />
            {onSummary && errorAside}
          </div>
        )}
        <div className="cluster">
          {position > 0 && <Button onClick={() => go(previousPosition(position))}>{t("wizard.previous")}</Button>}
          {onSummary ? (
            <Button type="submit" variant="primary" icon={submitIcon} disabled={busy} onClick={ignoreRepeatedClick}>
              {submitLabel}
            </Button>
          ) : (
            <Button type="submit" variant="primary" disabled={steps[position]?.blocked?.() === true}>
              {forwardLabel}
            </Button>
          )}
          <ModalCancel />
        </div>
      </form>
    </div>
  );
}

// La barre d'étapes : numéro et nom, la courante marquée. En modification,
// chaque étape y mène. Seule la courante porte son nom, en entier : les autres
// se réduisent à leur pastille (titre complet en title et pour les lecteurs
// d'écran), un nom ne se coupe jamais. Sous 860 px, elle se réduit à « Étape 2 sur 4 » : le
// nom est juste dessous, en tête de carte.
export function WizardBar({
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
                <button type="button" className="wizard-jump" title={stepTitle} onClick={() => onJump(index)}>
                  {content}
                </button>
              ) : (
                <span className="wizard-jump" title={stepTitle}>
                  {content}
                </span>
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
// « Modifier » qui y ramène. Une étape qui ne saisit rien (un guide) n'y
// figure pas.
function WizardSummary({ steps, onEdit }: { steps: WizardStep[]; onEdit: (target: number) => void }) {
  const t = useT();
  return (
    <div className="wizard-summary">
      {steps.map((step, index) => {
        const lines = step.summary?.() ?? [];
        if (lines.length === 0) {
          return null;
        }
        return (
          <section key={step.id} className="wizard-summary-step">
            <div className="wizard-summary-head">
              <h3 className="side-label inline-label">{step.title}</h3>
              <Button small variant="ghost" onClick={() => onEdit(index)} aria-label={t("wizard.edit_step", step.title)}>
                {t("wizard.edit")}
              </Button>
            </div>
            {lines.map((line) => (
              <KeyValue key={line.label} label={line.label} value={line.value} mono={line.mono ?? false} />
            ))}
          </section>
        );
      })}
    </div>
  );
}
