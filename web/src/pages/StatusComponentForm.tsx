import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { useDeclaredServices } from "../api/directory";
import { useJobs } from "../api/jobs";
import { useMachines } from "../api/machines";
import { useProbes } from "../api/probes";
import { deleteStatusComponent, saveStatusComponent, useStatusComponent, type MemberKind, type StatusComponent, type StatusMember } from "../api/status";
import { Button } from "../components/Button";
import { Loaded } from "../components/Loaded";
import { EnumSelect, Field, Input, Select } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { nameWithMachine } from "../lib/objects";
import { componentFormOf, componentRequestOf, emptyComponentForm, memberKinds, type MemberChoice } from "../lib/status";
import { StatusPagePill } from "./status/StatusPagePill";
import "./StatusAdmin.scss";

interface Candidate {
  id: string;
  name: string;
}

// Créer ou modifier un composant : un nom public, un ordre, et les objets
// rattachés, choisis parmi ce qu'openCloud surveille déjà.
export function StatusComponentForm() {
  const { id } = useParams();
  const component = useStatusComponent(id ?? null);
  if (id === undefined) {
    return <ComponentForm existing={null} />;
  }
  return <Loaded resource={component}>{(data) => <ComponentForm existing={data} />}</Loaded>;
}

function ComponentForm({ existing }: { existing: StatusComponent | null }) {
  const t = useT();
  const navigate = useNavigate();
  const machines = useMachines();
  // Un membre « service » est un service déclaré : il survit à la recréation de ses conteneurs.
  const services = useDeclaredServices();
  const jobs = useJobs();
  const probes = useProbes();
  const form = useForm(existing === null ? emptyComponentForm() : componentFormOf(existing));
  const { values, change } = form;
  const action = useAction();
  const [pickKind, setPickKind] = useState<MemberKind>("probe");
  const [pickID, setPickID] = useState("");

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
      onDone: () => void navigate("/page-statut"),
    });
  };

  const remove = () => {
    if (!existing) {
      return;
    }
    action.run(() => deleteStatusComponent(existing.id), {
      confirm: t("component.delete_confirm", existing.name),
      topic: "status",
      onDone: () => void navigate("/page-statut"),
    });
  };

  return (
    <>
      <PageHead
        title={existing ? t("component.edit_title") : t("component.new_title")}
        subtitle={t("component.new_subtitle")}
        actions={
          existing && (
            <Button variant="danger" icon="x" onClick={remove}>
              {t("component.delete")}
            </Button>
          )
        }
      />
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={existing ? t("component.save") : t("component.create")}
            submitIcon={existing ? "check" : "plus"}
            busy={action.busy}
            cancelLabel={t("component.cancel")}
            cancelTo="/page-statut"
          />
        }
      >
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
