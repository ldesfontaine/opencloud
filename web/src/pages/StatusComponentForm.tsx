import { useEffect, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";

import { useDeclaredServices } from "../api/directory";
import { useJobs } from "../api/jobs";
import { useMachines } from "../api/machines";
import { useProbes } from "../api/probes";
import { deleteStatusComponent, fetchComponentDeletePlan, saveStatusComponent, useStatusComponent, useStatusPages, type MemberKind, type StatusComponent, type StatusMember } from "../api/status";
import { Button } from "../components/Button";
import { DeletePlanDialog } from "../components/DeletePlanDialog";
import { Loaded } from "../components/Loaded";
import { EnumSelect, Field, Input, Select } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { nameWithMachine } from "../lib/objects";
import { chosenPageID, componentFormOf, componentRequestOf, emptyComponentForm, memberKinds, pageName, type MemberChoice } from "../lib/status";
import { StatusPagePill } from "./status/StatusPagePill";
import "./StatusAdmin.scss";

interface Candidate {
  id: string;
  name: string;
}

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
  const machines = useMachines();
  // Un membre « service » est un service déclaré : il survit à la recréation de ses conteneurs.
  const services = useDeclaredServices();
  const jobs = useJobs();
  const probes = useProbes();
  const form = useForm(existing === null ? emptyComponentForm(pageID) : componentFormOf(existing));
  // Le retour mène aux composants de la page du formulaire.
  const back = `/page-statut/composants?page=${encodeURIComponent(form.values.pageID)}`;
  const { values, change } = form;
  const action = useAction();
  const [pickKind, setPickKind] = useState<MemberKind>("probe");
  const [pickID, setPickID] = useState("");
  const [removing, setRemoving] = useState(false);

  const candidates: Record<MemberKind, Candidate[]> = {
    machine: machines.data?.machines ?? [],
    service: (services.data?.services ?? []).map((service) => ({ id: service.id, name: nameWithMachine({ name: service.title !== "" ? service.title : service.name, machine_name: service.machine_name }) })),
    heartbeat: (jobs.data?.jobs ?? []).map((job) => ({ id: job.id, name: nameWithMachine(job) })),
    probe: (probes.data?.probes ?? []).map((probe) => ({ id: probe.id, name: nameWithMachine(probe) })),
  };
  const known = new Map<string, StatusMember>((existing?.members ?? []).map((member) => [`${member.kind}:${member.id}`, member]));
  const nameOf = (choice: MemberChoice) => candidates[choice.kind].find((candidate) => candidate.id === choice.id)?.name ?? known.get(`${choice.kind}:${choice.id}`)?.name ?? t("component.missing");
  const available = candidates[pickKind].filter((candidate) => !values.members.some((member) => member.kind === pickKind && member.id === candidate.id));

  useEffect(() => {
    setPickID("");
  }, [pickKind]);

  const add = () => {
    if (pickID === "") {
      return;
    }
    change("members", [...values.members, { kind: pickKind, id: pickID }]);
    setPickID("");
  };

  const save = () => {
    const body = componentRequestOf(values);
    action.run(() => saveStatusComponent(existing?.id, body), {
      topic: "status",
      onDone: () => void navigate(back),
    });
  };

  return (
    <>
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
          {values.members.length === 0 ? (
            <span className="secondary">{t("component.objects_empty")}</span>
          ) : (
            <div className="members">
              {values.members.map((member) => {
                const detail = known.get(`${member.kind}:${member.id}`);
                return (
                  <div className="member" key={`${member.kind}:${member.id}`}>
                    <span className="kind">{t(`component.kind_${member.kind}`)}</span>
                    <span className="medium">{nameOf(member)}</span>
                    {detail ? <MemberPill member={detail} /> : <span />}
                    <Button
                      small
                      variant="ghost"
                      icon="x"
                      onClick={() => change("members", values.members.filter((candidate) => candidate !== member))}
                      aria-label={t("component.remove_object")}
                    />
                  </div>
                );
              })}
            </div>
          )}
          <div className="member-pick">
            <EnumSelect value={pickKind} values={memberKinds} labelKey="component.kind_" onChange={setPickKind} aria-label={t("component.objects_label")} />
            <Select value={pickID} onChange={(event) => setPickID(event.target.value)} aria-label={t("component.pick_object")}>
              <option value="">{t("component.pick_object")}</option>
              {available.map((candidate) => (
                <option key={candidate.id} value={candidate.id}>
                  {candidate.name}
                </option>
              ))}
            </Select>
            <Button icon="plus" onClick={add} disabled={pickID === ""}>
              {t("component.add_object")}
            </Button>
          </div>
        </Field>
      </FormCard>
    </>
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
