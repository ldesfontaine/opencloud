import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { api, ApiError } from "../api/client";
import type { Probe, ProbeResponse, ProbeResult } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note, Stat } from "../components/Card";
import { CertificatePill, Remaining, TrustPill } from "../components/Certificate";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Dot, ProbePill } from "../components/Pill";
import { Table } from "../components/Table";
import { UptimeBar } from "../components/UptimeBar";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useCertificateThresholds, useI18n, useT, type Translate } from "../i18n/context";
import { read } from "../lib/certificates";
import { probeTones, uptimePercent } from "../lib/probes";
import { useRefresh } from "../lib/refresh";
import { ago, formatClock, formatDuration } from "../lib/time";
import { formatDecimal } from "../lib/units";
import { NotFound } from "./NotFound";

// Les jours montrés sur la fiche : autant que l'agrégat en garde de lisible.
const detailSpan = 90;

function checksLabel(t: Translate, total: number): string {
  return total === 1 ? t("probe.checks_one") : t("probe.checks", total);
}

// La fiche d'une sonde : son état, sa disponibilité par fenêtre, sa barre
// jour par jour et ses derniers essais.
export function ProbePage() {
  const { id = "" } = useParams();
  const response = useResource<ProbeResponse>(`/api/probes/${id}`);
  if (response.error instanceof ApiError && response.error.status === 404) {
    return <NotFound />;
  }
  if (!response.data) {
    return response.error ? <Failure error={response.error} /> : null;
  }
  return <ProbeView response={response.data} reload={response.reload} />;
}

function ProbeView({ response, reload }: { response: ProbeResponse; reload: () => void }) {
  const { t, language } = useI18n();
  const now = useNow();
  const probe = response.probe;
  return (
    <>
      <PageHead title={probe.name} subtitle={probe.target} actions={<Actions probe={probe} reload={reload} />} />
      <Card>
        <CardBody gap={16}>
          <span className="machine-state">
            <ProbePill status={probe.status} />
            <span className="meta">
              <Link to={`/machines/${probe.machine_id}`}>{probe.machine_name}</Link>
              {probe.service_id !== "" && (
                <>
                  <span className="sep" />
                  <Link to={`/services/${probe.service_id}`}>{probe.service_name}</Link>
                </>
              )}
              <span className="sep" />
              <span>
                {t("probe.every")} <span className="mono">{formatDuration(t, probe.interval_seconds * 1000)}</span>
              </span>
              <span className="sep" />
              <span>
                {t("probe.last_check")} {probe.last_checked_at === null ? t("probe.never") : ago(t, probe.last_checked_at, now)}
              </span>
              {probe.last_checked_at !== null && (
                <>
                  <span className="sep" />
                  <span className="mono">{t("time.milliseconds", probe.last_duration_ms)}</span>
                </>
              )}
              {probe.last_reason !== "" && (
                <>
                  <span className="sep" />
                  <span className="mono">{t(`probe.reason_${probe.last_reason}`)}</span>
                </>
              )}
            </span>
          </span>
          {probe.status === "paused" && <Note tone="warn">{t("probe.paused_text")}</Note>}
          {probe.status !== "paused" && !probe.machine_online && <Note tone="warn">{t("probe.machine_offline")}</Note>}
          {probe.status === "degraded" && <Note tone="warn">{t("probe.degraded_text")}</Note>}
        </CardBody>
      </Card>
      <div className="grid-4">
        {response.uptime.map((window) => {
          const percent = uptimePercent(window.total, window.success);
          return (
            <Stat
              key={window.window}
              icon="network"
              label={t(`probe.window_${window.window}`)}
              value={percent === null ? "–" : formatDecimal(language, percent, percent === 100 ? 0 : 2)}
              unit={percent === null ? undefined : "%"}
              foot={percent === null ? t("probe.uptime_none") : checksLabel(t, window.total)}
            />
          );
        })}
      </div>
      <Card>
        <CardHeader title={t("probe.bar_title")} aside={t("probe.bar_text")} />
        <CardBody>
          {response.days.length > 0 ? <UptimeBar days={response.days} span={detailSpan} now={now} /> : <Empty text={t("probe.bar_empty")} />}
        </CardBody>
      </Card>
      <div className={probe.kind === "http" || probe.tls ? "grid-2" : undefined}>
        <Card>
          <CardHeader title={t("tab.summary")} aside={t("probe.uptime_text")} />
          <CardBody gap={10}>
            <KeyValue label={t("probe.field_target")} value={probe.target} mono />
            <KeyValue label={t("probe.field_kind")} value={t(`probe.kind_${probe.kind}`)} />
            <KeyValue label={t("probe.field_machine")} value={<Link to={`/machines/${probe.machine_id}`}>{probe.machine_name}</Link>} />
            {probe.service_id !== "" && (
              <KeyValue label={t("probe.field_service")} value={<Link to={`/services/${probe.service_id}`}>{probe.service_name}</Link>} />
            )}
            <KeyValue label={t("probe.field_interval")} value={formatDuration(t, probe.interval_seconds * 1000)} mono />
            <KeyValue label={t("probe.field_timeout")} value={formatDuration(t, probe.timeout_seconds * 1000)} mono />
            <KeyValue label={t("probe.field_thresholds")} value={`${probe.failure_threshold} / ${probe.recovery_threshold}`} mono />
            {probe.kind === "http" && (
              <>
                <KeyValue label={t("probe.field_method")} value={probe.method} mono />
                <KeyValue label={t("probe.field_expected_status")} value={probe.expected_status} mono />
                {probe.expected_body !== "" && <KeyValue label={t("probe.field_expected_body")} value={probe.expected_body} mono />}
                <KeyValue label={t("probe.field_redirects")} value={t(probe.follow_redirects ? "probe.on" : "probe.off")} />
              </>
            )}
            <KeyValue label={t("probe.field_created")} value={formatClock(probe.created_at)} mono />
          </CardBody>
        </Card>
        {/* Une sonde TCP qui ne fait pas de poignée de main ne verra jamais
            de certificat : la carte ne se pose que là où il peut y en avoir un. */}
        {(probe.kind === "http" || probe.tls) && <CertificateCard probe={probe} />}
      </div>
      <Card className="scroll-x">
        <CardHeader title={t("probe.results_title")} aside={t("probe.results_text")} />
        {response.results.length > 0 ? (
          <Table>
            <thead>
              <tr>
                <th>{t("probe.col_when")}</th>
                <th>{t("probe.col_outcome")}</th>
                <th>{t("probe.col_duration")}</th>
                <th>{t("probe.col_code")}</th>
                <th>{t("probe.col_reason")}</th>
              </tr>
            </thead>
            <tbody>
              {response.results.map((result) => (
                <ResultRow key={result.checked_at} result={result} />
              ))}
            </tbody>
          </Table>
        ) : (
          <Empty text={t("probe.results_empty")} />
        )}
      </Card>
    </>
  );
}

// Le certificat vu : ce qu'il reste, et ce qu'on a pu vérifier. L'échéance
// et la confiance se lisent séparément — un certificat d'autorité interne
// garde une date parfaitement claire, et c'est le cas où l'on se fait avoir.
function CertificateCard({ probe }: { probe: Probe }) {
  const t = useT();
  const now = useNow();
  const thresholds = useCertificateThresholds();
  const certificate = probe.certificate;
  if (certificate === null) {
    return (
      <Card>
        <CardHeader title={t("probe.cert_title")} aside={t("probe.cert_text")} />
        <Empty text={t("probe.cert_none")} />
      </Card>
    );
  }
  const reading = read(certificate, thresholds, now);
  return (
    <Card>
      <CardHeader title={t("probe.cert_title")} aside={t("probe.cert_text")} />
      <CardBody gap={10}>
        <div className="kv">
          <span className="cluster">
            <CertificatePill reading={reading} />
            <TrustPill reading={reading} />
          </span>
          <Remaining reading={reading} />
        </div>
        <KeyValue label={t("probe.cert_subject")} value={certificate.subject} mono />
        <KeyValue label={t("probe.cert_issuer")} value={certificate.issuer} mono />
        <KeyValue label={t("probe.cert_since")} value={formatClock(certificate.not_before)} mono />
        <KeyValue label={t("probe.cert_until")} value={formatClock(certificate.not_after)} mono />
        <KeyValue label={t("cert.field_chain")} value={t(certificate.chain_valid ? "cert.chain_valid" : "cert.chain_invalid")} />
        <KeyValue label={t("cert.field_name")} value={t(certificate.hostname_match ? "cert.name_match" : "cert.name_mismatch")} />
        {/* Rien à dire quand la cible n'agrafe pas : personne n'a été
            contacté pour le savoir. */}
        {certificate.ocsp !== "" && <KeyValue label={t("cert.field_ocsp")} value={t(`cert.ocsp_${certificate.ocsp}`)} />}
        <KeyValue label={t("cert.field_fingerprint")} value={certificate.fingerprint} mono />
        {reading.untrusted && <Note tone="warn">{t("cert.untrusted_text")}</Note>}
        {reading.revoked && <Note tone="danger">{t("cert.revoked_text")}</Note>}
      </CardBody>
    </Card>
  );
}

function ResultRow({ result }: { result: ProbeResult }) {
  const t = useT();
  return (
    <tr>
      <td className="num nowrap">{formatClock(result.checked_at)}</td>
      <td>
        <span className="svc">
          <Dot tone={probeTones[result.outcome]} />
          {t(`probe.outcome_${result.outcome}`)}
        </span>
      </td>
      <td className="num">{t("time.milliseconds", result.duration_ms)}</td>
      <td className="num">{result.code === null ? "—" : result.code}</td>
      <td className="secondary">{result.reason === "" ? "—" : t(`probe.reason_${result.reason}`)}</td>
    </tr>
  );
}

// Mettre en pause, reprendre, supprimer après confirmation.
function Actions({ probe, reload }: { probe: Probe; reload: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [error, setError] = useState<unknown>(null);

  const act = (action: "pause" | "resume") => {
    api
      .post(`/api/probes/${probe.id}/actions/${action}`)
      .then(() => {
        reload();
        refresh();
      })
      .catch(setError);
  };
  const remove = () => {
    if (!window.confirm(t("probe.delete_confirm", probe.name))) {
      return;
    }
    api
      .delete(`/api/probes/${probe.id}`)
      .then(() => {
        refresh();
        void navigate("/domaines");
      })
      .catch(setError);
  };

  return (
    <>
      {error !== null && <Failure error={error} />}
      {probe.status === "paused" ? (
        <Button icon="refresh" onClick={() => act("resume")}>
          {t("probe.resume")}
        </Button>
      ) : (
        <Button icon="pause" onClick={() => act("pause")}>
          {t("probe.pause")}
        </Button>
      )}
      <Button variant="danger" icon="x" onClick={remove}>
        {t("probe.delete")}
      </Button>
    </>
  );
}
