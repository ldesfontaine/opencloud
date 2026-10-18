import { useEffect, useState, type FormEvent } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";

import { api, errorKey } from "../api/client";
import type { DeclaredServicesResponse, MachinesResponse, Probe, ProbeKind, ProbeResponse, ContainerResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { emptyProbeForm, probeFormOf, probeRequestOf, type ProbeFormValues } from "../lib/probes";
import { useRefresh } from "../lib/refresh";
import { isPublicIP } from "../lib/containers";
import { NotFound } from "./NotFound";

// Créer ou modifier une sonde : l'opérateur dit ce qu'il faut joindre, tous
// les combien, et depuis quelle machine ; le serveur valide et répond par la
// clé du refus. La sonde vise un service déclaré de l'annuaire, sur n'importe
// quelle machine : venu de sa fiche (?service=), ou d'un conteneur
// (?service=&container=), le formulaire de création arrive déjà rempli. En
// édition, il part de la sonde et la remplace entière ; ce qui change ce
// qu'elle mesure la remet à « Nouveau ».
export function ProbeForm() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [params] = useSearchParams();
  const fromService = params.get("service") ?? "";
  const fromContainer = params.get("container") ?? "";
  const machines = useResource<MachinesResponse>("/api/machines");
  const declared = useResource<DeclaredServicesResponse>("/api/directory/services");
  const container = useResource<ContainerResponse>(fromContainer === "" ? null : `/api/containers/${fromContainer}`);
  const existing = useResource<ProbeResponse>(isEditing ? `/api/probes/${id}` : null);

  const [values, setValues] = useState<ProbeFormValues>(emptyProbeForm);
  const [serviceID, setServiceID] = useState(fromService);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [prefilled, setPrefilled] = useState(false);
  // La sonde se lit une fois : une relecture (le signal de rafraîchissement
  // passe) ne doit pas écraser ce que l'opérateur est en train de taper.
  const [filled, setFilled] = useState(false);

  const change = <Key extends keyof ProbeFormValues>(key: Key, value: ProbeFormValues[Key]) => {
    setValues((current) => ({ ...current, [key]: value }));
  };

  useEffect(() => {
    if (filled || existing.data === null) {
      return;
    }
    setFilled(true);
    setValues(probeFormOf(existing.data.probe));
    setServiceID(existing.data.probe.service_id);
  }, [existing.data, filled]);

  // À défaut de choix, la machine openCloud sonde : c'est la vue depuis
  // l'extérieur, celle qu'on veut presque toujours.
  const local = machines.data?.machines.find((machine) => machine.local);
  useEffect(() => {
    if (!isEditing && values.machineID === "" && local !== undefined) {
      setValues((current) => ({ ...current, machineID: local.id }));
    }
  }, [isEditing, local, values.machineID]);

  // Venue d'un service déclaré, la sonde en prend le nom. Venue d'un de ses
  // conteneurs, elle vise en plus son premier port publié depuis la machine
  // qui le porte : la vue depuis l'intérieur, qui marche derrière un NAT
  // comme ailleurs.
  const service = declared.data?.services.find((candidate) => candidate.id === fromService);
  const carrier = container.data?.container;
  const ready = service !== undefined && (fromContainer === "" || carrier !== undefined);
  useEffect(() => {
    if (prefilled || fromService === "" || !ready) {
      return;
    }
    setPrefilled(true);
    setValues((current) => {
      const next = { ...current, name: service.title !== "" ? service.title : service.name };
      // La machine qui porte le conteneur sonde, port publié ou non : sans
      // port il n'y a pas de cible à deviner, mais la vue depuis l'intérieur
      // reste celle qu'on veut.
      if (carrier !== undefined) {
        next.machineID = carrier.machine_id;
      }
      const port = carrier?.ports[0];
      if (carrier !== undefined && port !== undefined) {
        const host = isPublicIP(port.ip) ? "127.0.0.1" : port.ip;
        next.target = `http://${host}:${String(port.host_port)}/`;
      }
      return next;
    });
  }, [fromService, ready, service, carrier, prefilled]);

  if (isEditing && existing.error?.status === 404) {
    return <NotFound />;
  }

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const body = probeRequestOf(values, serviceID);
    const request = isEditing ? api.put<Probe>(`/api/probes/${id}`, body) : api.post<Probe>("/api/probes", body);
    request
      .then((saved) => {
        refresh();
        void navigate(`/domaines/${saved.id}`);
      })
      .catch((failed: unknown) => setError(errorKey(failed)))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <PageHead
        title={isEditing ? t("probe.edit_title") : t("probe.new_title")}
        subtitle={isEditing ? t("probe.edit_subtitle") : t("probe.new_subtitle")}
      />
      {existing.error && <Failure error={existing.error} />}
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("probe.name_label")} htmlFor="probe-name" help={t("probe.name_help")}>
            <Input
              id="probe-name"
              type="text"
              value={values.name}
              onChange={(event) => change("name", event.target.value)}
              placeholder={t("probe.name_placeholder")}
              maxLength={80}
              autoComplete="off"
              autoFocus
              required
            />
          </Field>
          <div className="grid-2">
            <Field label={t("probe.kind_label")} htmlFor="probe-kind">
              <Select id="probe-kind" value={values.kind} onChange={(event) => change("kind", event.target.value as ProbeKind)}>
                <option value="http">{t("probe.kind_http")}</option>
                <option value="tcp">{t("probe.kind_tcp")}</option>
              </Select>
            </Field>
            <Field label={t("probe.machine_label")} htmlFor="probe-machine" help={t("probe.machine_help")}>
              <Select id="probe-machine" icon="server" value={values.machineID} onChange={(event) => change("machineID", event.target.value)}>
                {(machines.data?.machines ?? []).map((machine) => (
                  <option key={machine.id} value={machine.id}>
                    {machine.name}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <Field label={t("probe.service_label")} htmlFor="probe-service" help={t("probe.service_help")}>
            <Select id="probe-service" value={serviceID} onChange={(event) => setServiceID(event.target.value)}>
              <option value="">{t("probe.service_none")}</option>
              {(declared.data?.services ?? []).map((candidate) => (
                <option key={candidate.id} value={candidate.id}>
                  {t("probe.service_option", candidate.title !== "" ? candidate.title : candidate.name, candidate.machine_name)}
                </option>
              ))}
            </Select>
          </Field>
          <Field
            label={t("probe.target_label")}
            htmlFor="probe-target"
            help={t(values.kind === "http" ? "probe.target_help_http" : "probe.target_help_tcp")}
          >
            <Input
              id="probe-target"
              mono
              type="text"
              value={values.target}
              onChange={(event) => change("target", event.target.value)}
              placeholder={values.kind === "http" ? t("probe.target_placeholder_http") : t("probe.target_placeholder_tcp")}
              maxLength={512}
              autoComplete="off"
              required
            />
          </Field>
          <div className="grid-2">
            <Field label={t("probe.interval_label")} htmlFor="probe-interval" help={t("probe.interval_help")}>
              <Input id="probe-interval" mono type="number" value={values.interval} onChange={(event) => change("interval", event.target.value)} min={30} max={86400} required />
            </Field>
            <Field label={t("probe.timeout_label")} htmlFor="probe-timeout" help={t("probe.timeout_help")}>
              <Input id="probe-timeout" mono type="number" value={values.timeout} onChange={(event) => change("timeout", event.target.value)} min={1} max={30} required />
            </Field>
          </div>
          <div className="grid-2">
            <Field label={t("probe.failure_label")} htmlFor="probe-failure" help={t("probe.failure_help")}>
              <Input id="probe-failure" mono type="number" value={values.failure} onChange={(event) => change("failure", event.target.value)} min={1} max={10} required />
            </Field>
            <Field label={t("probe.recovery_label")} htmlFor="probe-recovery" help={t("probe.recovery_help")}>
              <Input id="probe-recovery" mono type="number" value={values.recovery} onChange={(event) => change("recovery", event.target.value)} min={1} max={10} required />
            </Field>
          </div>
          {values.kind === "http" && (
            <>
              <div className="grid-2">
                <Field label={t("probe.method_label")} htmlFor="probe-method">
                  <Select id="probe-method" value={values.method} onChange={(event) => change("method", event.target.value)}>
                    <option value="GET">GET</option>
                    <option value="HEAD">HEAD</option>
                    <option value="POST">POST</option>
                  </Select>
                </Field>
                <Field label={t("probe.expected_status_label")} htmlFor="probe-status" help={t("probe.expected_status_help")}>
                  <Input id="probe-status" mono type="text" value={values.expectedStatus} onChange={(event) => change("expectedStatus", event.target.value)} maxLength={40} autoComplete="off" />
                </Field>
              </div>
              <div className="grid-2">
                <Field label={t("probe.expected_body_label")} htmlFor="probe-body" help={t("probe.expected_body_help")}>
                  <Input id="probe-body" type="text" value={values.expectedBody} onChange={(event) => change("expectedBody", event.target.value)} maxLength={200} autoComplete="off" />
                </Field>
                <Field label={t("probe.follow_redirects_label")} htmlFor="probe-redirects">
                  <Select id="probe-redirects" value={values.redirects} onChange={(event) => change("redirects", event.target.value as ProbeFormValues["redirects"])}>
                    <option value="yes">{t("probe.on")}</option>
                    <option value="no">{t("probe.off")}</option>
                  </Select>
                </Field>
              </div>
            </>
          )}
          {values.kind === "tcp" && (
            <Field label={t("probe.tls_label")} htmlFor="probe-tls" help={t("probe.tls_help")}>
              <Select id="probe-tls" value={values.useTLS} onChange={(event) => change("useTLS", event.target.value as ProbeFormValues["useTLS"])}>
                <option value="no">{t("probe.off")}</option>
                <option value="yes">{t("probe.on")}</option>
              </Select>
            </Field>
          )}
          {isEditing && <Note tone="warn">{t("probe.edit_reset_note")}</Note>}
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon={isEditing ? "check" : "plus"} disabled={busy || (isEditing && !filled)}>
              {isEditing ? t("probe.save") : t("probe.create")}
            </Button>
            <Button variant="ghost" to={isEditing ? `/domaines/${id}` : "/domaines"}>
              {t("probe.cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
