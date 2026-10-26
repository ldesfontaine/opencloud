import { useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";

import { deleteStatusComponent, fetchComponentDeletePlan, saveStatusComponent, useStatusComponent, useStatusPages, type StatusComponent, type StatusMember } from "../api/status";
import { Button } from "../components/Button";
import { DeletePlanDialog } from "../components/DeletePlanDialog";
import { Loaded } from "../components/Loaded";
import { Field, Input, Select } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { FormPage } from "../components/FormPage";
import { ObjectPicker } from "../components/ObjectPicker";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { chosenPageID, componentFormOf, componentRequestOf, emptyComponentForm, pageName } from "../lib/status";
import { StatusPagePill } from "./status/StatusPagePill";

// Créer ou modifier un composant : sa page (à la création), un nom public,
// un ordre, et les objets rattachés, choisis parmi ce qu'openCloud surveille
// déjà. Une création part de la page de ?page=.
export function StatusComponentForm() {
  const { id } = useParams();
  const [params] = useSearchParams();
  const component = useStatusComponent(id ?? null);
  const pages = useStatusPages();
  if (id === undefined) {
    return <Loaded resource={pages}>{(data) => <ComponentForm existing={null} pageID={chosenPageID(data.pages, params.get("page")) ?? ""} />}</Loaded>;
  }
  return <Loaded resource={component}>{(data) => <ComponentForm existing={data} pageID={data.page_id} />}</Loaded>;
}

function ComponentForm({ existing, pageID }: { existing: StatusComponent | null; pageID: string }) {
  const t = useT();
  const navigate = useNavigate();
  const pages = useStatusPages();
  const form = useForm(existing === null ? emptyComponentForm(pageID) : componentFormOf(existing));
  // Le retour mène aux composants de la page du formulaire.
  const back = `/page-statut/composants?page=${encodeURIComponent(form.values.pageID)}`;
  const { values, change } = form;
  const action = useAction();
  const [removing, setRemoving] = useState(false);

  const known = new Map<string, StatusMember>((existing?.members ?? []).map((member) => [`${member.kind}:${member.id}`, member]));

  const save = () => {
    const body = componentRequestOf(values);
    action.run(() => saveStatusComponent(existing?.id, body), {
      topic: "status",
      onDone: () => void navigate(back),
    });
  };

  return (
    <FormPage>
      <PageHead
        title={existing ? t("component.edit_title") : t("component.new_title")}
        subtitle={t("component.new_subtitle")}
        actions={
          existing && (
            <Button variant="danger" icon="x" onClick={() => setRemoving(true)}>
              {t("component.delete")}
            </Button>
          )
        }
      />
      {removing && existing && (
        <DeletePlanDialog
          question={t("component.delete_confirm", existing.name)}
          confirmLabel={t("component.delete")}
          loadPlan={() => fetchComponentDeletePlan(existing.id)}
          perform={() => deleteStatusComponent(existing.id)}
          topic="status"
          onDone={() => void navigate(back)}
          onClose={() => setRemoving(false)}
        />
      )}
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={existing ? t("component.save") : t("component.create")}
            submitIcon={existing ? "check" : "plus"}
            busy={action.busy}
            cancelLabel={t("component.cancel")}
            cancelTo={back}
          />
        }
      >
        <Field label={t("component.page_label")} htmlFor="component-page" help={t("component.page_help")}>
          <Select id="component-page" value={values.pageID} onChange={(event) => change("pageID", event.target.value)} disabled={existing !== null}>
            {(pages.data?.pages ?? []).map((page) => (
              <option key={page.id} value={page.id}>
                {pageName(page)}
              </option>
            ))}
          </Select>
        </Field>
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
      </FormCard>
    </FormPage>
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
  return <StatusPagePill state={member.state} />;
}
