import { useEffect, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router";

import { api, ApiError, errorKey } from "../api/client";
import type { JobsResponse, MachinesResponse, MemberKind, ProbesResponse, ServicesResponse, StatusComponent, StatusMember } from "../api/types";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { useRefresh } from "../lib/refresh";
import { NotFound } from "./NotFound";
import { StatePill } from "./StatusAdmin";
import "./StatusAdmin.scss";

const kinds: MemberKind[] = ["machine", "service", "heartbeat", "probe"];

interface Candidate {
  id: string;
  name: string;
}

interface Draft {
  kind: MemberKind;
  id: string;
}

// Créer ou modifier un composant : un nom public, un ordre, et les objets
// rattachés, choisis parmi ce qu'openCloud surveille déjà.
export function StatusComponentForm() {
  const { id } = useParams();
  const component = useResource<StatusComponent>(id !== undefined ? `/api/status/components/${id}` : null);
  if (component.error instanceof ApiError && component.error.status === 404) {
    return <NotFound />;
  }
  if (id !== undefined && !component.data) {
    return component.error ? <Failure error={component.error} /> : null;
  }
  return <ComponentForm existing={component.data} />;
}

function ComponentForm({ existing }: { existing: StatusComponent | null }) {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const machines = useResource<MachinesResponse>("/api/machines");
  const services = useResource<ServicesResponse>("/api/services");
  const jobs = useResource<JobsResponse>("/api/jobs");
  const probes = useResource<ProbesResponse>("/api/probes");
  const [name, setName] = useState(existing?.name ?? "");
  const [position, setPosition] = useState(String(existing?.position ?? 0));
  const [members, setMembers] = useState<Draft[]>(existing?.members.map((member) => ({ kind: member.kind, id: member.id })) ?? []);
  const [pickKind, setPickKind] = useState<MemberKind>("probe");
  const [pickID, setPickID] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const candidates: Record<MemberKind, Candidate[]> = {
    machine: machines.data?.machines ?? [],
    service: services.data?.services ?? [],
    heartbeat: jobs.data?.jobs ?? [],
    probe: probes.data?.probes ?? [],
  };
  const known = new Map<string, StatusMember>((existing?.members ?? []).map((member) => [`${member.kind}:${member.id}`, member]));
  const nameOf = (draft: Draft) => candidates[draft.kind].find((candidate) => candidate.id === draft.id)?.name ?? known.get(`${draft.kind}:${draft.id}`)?.name ?? t("component.missing");
  const available = candidates[pickKind].filter((candidate) => !members.some((member) => member.kind === pickKind && member.id === candidate.id));

  useEffect(() => {
    setPickID("");
  }, [pickKind]);

  const add = () => {
    if (pickID === "") {
      return;
    }
    setMembers((current) => [...current, { kind: pickKind, id: pickID }]);
    setPickID("");
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const body = { name, position: Number(position), members };
    const request = existing ? api.put<StatusComponent>(`/api/status/components/${existing.id}`, body) : api.post<StatusComponent>("/api/status/components", body);
    request
      .then(() => {
        refresh("status");
        void navigate("/page-statut");
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  const remove = () => {
    if (!existing || !window.confirm(t("component.delete_confirm", existing.name))) {
      return;
    }
    api
      .delete(`/api/status/components/${existing.id}`)
      .then(() => {
        refresh("status");
        void navigate("/page-statut");
      })
      .catch((failure: unknown) => setError(errorKey(failure)));
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
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("component.name_label")} htmlFor="component-name" help={t("component.name_help")}>
            <Input id="component-name" type="text" value={name} onChange={(event) => setName(event.target.value)} placeholder="Site web" maxLength={80} autoComplete="off" autoFocus required />
          </Field>
          <Field label={t("component.position_label")} htmlFor="component-position" help={t("component.position_help")}>
            <Input id="component-position" mono type="number" value={position} onChange={(event) => setPosition(event.target.value)} min={0} max={1000} />
          </Field>
          <Field label={t("component.objects_label")} help={t("component.objects_help")}>
            {members.length === 0 ? (
              <span className="secondary">{t("component.objects_empty")}</span>
            ) : (
              <div className="members">
                {members.map((member) => {
                  const detail = known.get(`${member.kind}:${member.id}`);
                  return (
                    <div className="member" key={`${member.kind}:${member.id}`}>
                      <span className="kind">{t(`component.kind_${member.kind}`)}</span>
                      <span className="medium">{nameOf(member)}</span>
                      {detail ? <MemberPill member={detail} /> : <span />}
                      <Button small variant="ghost" icon="x" onClick={() => setMembers((current) => current.filter((candidate) => candidate !== member))} aria-label={t("component.remove_object")} />
                    </div>
                  );
                })}
              </div>
            )}
            <div className="member-pick">
              <Select value={pickKind} onChange={(event) => setPickKind(event.target.value as MemberKind)} aria-label={t("component.objects_label")}>
                {kinds.map((kind) => (
                  <option key={kind} value={kind}>
                    {t(`component.kind_${kind}`)}
                  </option>
                ))}
              </Select>
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
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon={existing ? "check" : "plus"} disabled={busy}>
              {existing ? t("component.save") : t("component.create")}
            </Button>
            <Button variant="ghost" to="/page-statut">
              {t("component.cancel")}
            </Button>
          </div>
        </form>
      </Card>
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
  return <StatePill state={member.state} />;
}
