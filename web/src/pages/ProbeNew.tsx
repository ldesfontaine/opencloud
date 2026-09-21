import { useEffect, useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { api, errorKey } from "../api/client";
import type { MachinesResponse, Probe, ProbeKind, ServicesResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { useRefresh } from "../lib/refresh";
import { isPublicIP } from "../lib/services";

// L'opérateur dit ce qu'il faut joindre, tous les combien, et depuis
// quelle machine ; le serveur valide et répond par la clé du refus. Venu
// d'une fiche de service, le formulaire arrive déjà rempli.
export function ProbeNew() {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [params] = useSearchParams();
  const fromService = params.get("service") ?? "";
  const machines = useResource<MachinesResponse>("/api/machines");
  const services = useResource<ServicesResponse>(fromService === "" ? null : "/api/services");

  const [name, setName] = useState("");
  const [kind, setKind] = useState<ProbeKind>("http");
  const [target, setTarget] = useState("");
  const [machineID, setMachineID] = useState("");
  const [interval, setInterval] = useState("60");
  const [timeout, setTimeout] = useState("10");
  const [failure, setFailure] = useState("3");
  const [recovery, setRecovery] = useState("2");
  const [method, setMethod] = useState("GET");
  const [expectedStatus, setExpectedStatus] = useState("2xx");
  const [expectedBody, setExpectedBody] = useState("");
  const [redirects, setRedirects] = useState("yes");
  // Une sonde TCP peut faire une poignée de main plutôt qu'une simple
  // connexion : c'est ce qui donne son certificat à un port chiffré qui ne
  // parle pas HTTP, SMTP ou IMAP.
  const [useTLS, setUseTLS] = useState("no");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [prefilled, setPrefilled] = useState(false);

  // À défaut de choix, la machine openCloud sonde : c'est la vue depuis
  // l'extérieur, celle qu'on veut presque toujours.
  const local = machines.data?.machines.find((machine) => machine.local);
  useEffect(() => {
    if (machineID === "" && local !== undefined) {
      setMachineID(local.id);
    }
  }, [local, machineID]);

  // Venue d'un service, la sonde vise son premier port publié depuis la
  // machine qui le porte : la vue depuis l'intérieur, qui marche derrière
  // un NAT comme ailleurs.
  const service = services.data?.services.find((candidate) => candidate.id === fromService);
  useEffect(() => {
    if (prefilled || service === undefined) {
      return;
    }
    setPrefilled(true);
    setName(service.name);
    setMachineID(service.machine_id);
    const port = service.ports[0];
    if (port !== undefined) {
      const host = isPublicIP(port.ip) ? "127.0.0.1" : port.ip;
      setTarget(`http://${host}:${String(port.host_port)}/`);
    }
  }, [service, prefilled]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    api
      .post<Probe>("/api/probes", {
        name,
        kind,
        target,
        machine_id: machineID,
        service_id: fromService,
        interval_seconds: Number(interval),
        timeout_seconds: Number(timeout),
        failure_threshold: Number(failure),
        recovery_threshold: Number(recovery),
        method: kind === "http" ? method : "",
        expected_status: kind === "http" ? expectedStatus : "",
        expected_body: kind === "http" ? expectedBody : "",
        follow_redirects: kind === "http" && redirects === "yes",
        tls: kind === "tcp" && useTLS === "yes",
      })
      .then((created) => {
        refresh();
        void navigate(`/domaines/${created.id}`);
      })
      .catch((failed: unknown) => setError(errorKey(failed)))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <PageHead title={t("probe.new_title")} subtitle={t("probe.new_subtitle")} />
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("probe.name_label")} htmlFor="probe-name" help={t("probe.name_help")}>
            <Input
              id="probe-name"
              type="text"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="site nextcloud"
              maxLength={80}
              autoComplete="off"
              autoFocus
              required
            />
          </Field>
          <div className="grid-2">
            <Field label={t("probe.kind_label")} htmlFor="probe-kind">
              <Select id="probe-kind" value={kind} onChange={(event) => setKind(event.target.value as ProbeKind)}>
                <option value="http">{t("probe.kind_http")}</option>
                <option value="tcp">{t("probe.kind_tcp")}</option>
              </Select>
            </Field>
            <Field label={t("probe.machine_label")} htmlFor="probe-machine" help={t("probe.machine_help")}>
              <Select id="probe-machine" icon="server" value={machineID} onChange={(event) => setMachineID(event.target.value)}>
                {(machines.data?.machines ?? []).map((machine) => (
                  <option key={machine.id} value={machine.id}>
                    {machine.name}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <Field
            label={t("probe.target_label")}
            htmlFor="probe-target"
            help={t(kind === "http" ? "probe.target_help_http" : "probe.target_help_tcp")}
          >
            <Input
              id="probe-target"
              mono
              type="text"
              value={target}
              onChange={(event) => setTarget(event.target.value)}
              placeholder={kind === "http" ? "https://cloud.exemple.fr/" : "10.8.0.2:5432"}
              maxLength={512}
              autoComplete="off"
              required
            />
          </Field>
          <div className="grid-2">
            <Field label={t("probe.interval_label")} htmlFor="probe-interval" help={t("probe.interval_help")}>
              <Input id="probe-interval" mono type="number" value={interval} onChange={(event) => setInterval(event.target.value)} min={30} max={86400} required />
            </Field>
            <Field label={t("probe.timeout_label")} htmlFor="probe-timeout" help={t("probe.timeout_help")}>
              <Input id="probe-timeout" mono type="number" value={timeout} onChange={(event) => setTimeout(event.target.value)} min={1} max={30} required />
            </Field>
          </div>
          <div className="grid-2">
            <Field label={t("probe.failure_label")} htmlFor="probe-failure" help={t("probe.failure_help")}>
              <Input id="probe-failure" mono type="number" value={failure} onChange={(event) => setFailure(event.target.value)} min={1} max={10} required />
            </Field>
            <Field label={t("probe.recovery_label")} htmlFor="probe-recovery" help={t("probe.recovery_help")}>
              <Input id="probe-recovery" mono type="number" value={recovery} onChange={(event) => setRecovery(event.target.value)} min={1} max={10} required />
            </Field>
          </div>
          {kind === "http" && (
            <>
              <div className="grid-2">
                <Field label={t("probe.method_label")} htmlFor="probe-method">
                  <Select id="probe-method" value={method} onChange={(event) => setMethod(event.target.value)}>
                    <option value="GET">GET</option>
                    <option value="HEAD">HEAD</option>
                    <option value="POST">POST</option>
                  </Select>
                </Field>
                <Field label={t("probe.expected_status_label")} htmlFor="probe-status" help={t("probe.expected_status_help")}>
                  <Input id="probe-status" mono type="text" value={expectedStatus} onChange={(event) => setExpectedStatus(event.target.value)} maxLength={40} autoComplete="off" />
                </Field>
              </div>
              <div className="grid-2">
                <Field label={t("probe.expected_body_label")} htmlFor="probe-body" help={t("probe.expected_body_help")}>
                  <Input id="probe-body" type="text" value={expectedBody} onChange={(event) => setExpectedBody(event.target.value)} maxLength={200} autoComplete="off" />
                </Field>
                <Field label={t("probe.follow_redirects_label")} htmlFor="probe-redirects">
                  <Select id="probe-redirects" value={redirects} onChange={(event) => setRedirects(event.target.value)}>
                    <option value="yes">{t("probe.on")}</option>
                    <option value="no">{t("probe.off")}</option>
                  </Select>
                </Field>
              </div>
            </>
          )}
          {kind === "tcp" && (
            <Field label={t("probe.tls_label")} htmlFor="probe-tls" help={t("probe.tls_help")}>
              <Select id="probe-tls" value={useTLS} onChange={(event) => setUseTLS(event.target.value)}>
                <option value="no">{t("probe.off")}</option>
                <option value="yes">{t("probe.on")}</option>
              </Select>
            </Field>
          )}
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon="plus" disabled={busy}>
              {t("probe.create")}
            </Button>
            <Button variant="ghost" to="/domaines">
              {t("probe.cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
