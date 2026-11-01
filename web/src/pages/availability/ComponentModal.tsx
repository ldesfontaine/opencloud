import { useState } from "react";
import { useNavigate, useParams } from "react-router";

import { deleteStatusComponent, fetchComponentDeletePlan, saveStatusComponent, useStatusComponent, type StatusComponent, type StatusMember } from "../../api/status";
import { Button } from "../../components/Button";
import { DeletePlanDialog } from "../../components/DeletePlanDialog";
import { Failure } from "../../components/Failure";
import { Field, Input } from "../../components/Field";
import { ModalForm } from "../../components/ModalForm";
import { ObjectPicker } from "../../components/ObjectPicker";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { isFormModified } from "../../lib/modal";
import { componentFormOf, componentRequestOf, emptyComponentForm, publicPagePath } from "../../lib/status";
import { StatusStatePill } from "./StatusStatePill";

// Ajouter ou modifier un composant : la fenêtre de la page publique d'un
// projet, à /disponibilite/pages/:id/composants/nouveau ou
// …/composants/:componentId. Le projet est celui de l'adresse : un composant
// ne change jamais de projet. Un nom public, un ordre, et les objets
// rattachés, choisis parmi ce qu'openCloud surveille déjà.
export function ComponentModal() {
  const { id = "", componentId } = useParams();
  const t = useT();
  const navigate = useNavigate();
  const isEditing = componentId !== undefined;
  const existing = useStatusComponent(componentId ?? null);
  const form = useForm(emptyComponentForm(id), isEditing ? { source: existing.data, fill: componentFormOf } : null);
  const { values, change } = form;
  const action = useAction();
  const [removing, setRemoving] = useState(false);

  const back = publicPagePath(id);
  const close = () => void navigate(back, { replace: true });
  const save = () => {
    action.run(() => saveStatusComponent(componentId, componentRequestOf(values)), { topic: "status", onDone: close });
  };

  const known = new Map<string, StatusMember>((existing.data?.members ?? []).map((member) => [`${member.kind}:${member.id}`, member]));
  const base = existing.data === null ? emptyComponentForm(id) : componentFormOf(existing.data);
  return (
    <>
      <ModalForm
        title={isEditing ? t("component.edit_title") : t("component.new_title")}
        onClose={close}
        dirty={form.isFilled && isFormModified(values, base)}
        wide
        onSubmit={save}
        submitLabel={isEditing ? t("component.save") : t("component.create")}
        submitIcon={isEditing ? "check" : "plus"}
        busy={action.busy}
        canSubmit={form.isFilled}
        error={action.error}
      >
        <p className="secondary">{t("component.new_subtitle")}</p>
        <Failure error={existing.error} />
        <Field label={t("component.name_label")} htmlFor="component-name" help={t("component.name_help")}>
          <Input
            id="component-name"
            type="text"
            value={values.name}
            onChange={(event) => change("name", event.target.value)}
            placeholder={t("component.name_placeholder")}
            maxLength={80}
            autoComplete="off"
            autoFocus
            required
          />
        </Field>
        <Field label={t("component.position_label")} htmlFor="component-position" help={t("component.position_help")}>
          <Input id="component-position" mono type="number" value={values.position} onChange={(event) => change("position", event.target.value)} min={0} max={1000} />
        </Field>
        <Field label={t("component.objects_label")} help={t("component.objects_help")}>
          <ObjectPicker
            value={values.members}
            onChange={(members) => change("members", members)}
            labelKeyPrefix="component.kind_"
            label={t("component.objects_label")}
            addLabel={t("component.add_object")}
            emptyText={t("component.objects_empty")}
            nameOf={(choice) => known.get(`${choice.kind}:${choice.id}`)?.name ?? t("component.missing")}
            detailOf={(choice) => {
              const detail = known.get(`${choice.kind}:${choice.id}`);
              return detail ? <MemberPill member={detail} /> : null;
            }}
          />
        </Field>
        {existing.data !== null && (
          <div className="cluster">
            <Button variant="danger" icon="x" onClick={() => setRemoving(true)}>
              {t("component.delete")}
            </Button>
          </div>
        )}
      </ModalForm>
      {removing && existing.data !== null && <RemoveComponent component={existing.data} onDone={close} onClose={() => setRemoving(false)} />}
    </>
  );
}

// La suppression passe par le plan : ce qui part, ce qui reste.
function RemoveComponent({ component, onDone, onClose }: { component: StatusComponent; onDone: () => void; onClose: () => void }) {
  const t = useT();
  return (
    <DeletePlanDialog
      question={t("component.delete_confirm", component.name)}
      confirmLabel={t("component.delete")}
      loadPlan={() => fetchComponentDeletePlan(component.id)}
      perform={() => deleteStatusComponent(component.id)}
      topic="status"
      onDone={onDone}
      onClose={onClose}
    />
  );
}

// Ce qu'un objet apporte : sa pastille, ou pourquoi il ne compte pas.
function MemberPill({ member }: { member: StatusMember }) {
  const t = useT();
  if (!member.present) {
    return <Pill tone="danger">{t("component.missing")}</Pill>;
  }
  if (!member.counts) {
    return <Pill tone="neutral">{t("component.counts_no")}</Pill>;
  }
  return <StatusStatePill state={member.state} />;
}
