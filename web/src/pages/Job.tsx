import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { api, ApiError } from "../api/client";
import type { Job, JobResponse, Run } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "../components/Card";
import { CopyButton, TokenBox } from "../components/Copy";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Dot, JobPill } from "../components/Pill";
import { List, RowText, RowWhen, Table } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { lastResult, preview, runTones } from "../lib/jobs";
import { useRefresh } from "../lib/refresh";
import { ago, deadlineIn, formatClock, formatDuration, lastPing } from "../lib/time";
import { NotFound } from "./NotFound";

// La page d'une tâche : son état, l'URL de ping et les extraits à coller,
// puis les dernières exécutions et les derniers pings.
export function JobPage() {
  const { id = "" } = useParams();
  const job = useResource<JobResponse>(`/api/jobs/${id}`);
  if (job.error instanceof ApiError && job.error.status === 404) {
    return <NotFound />;
  }
  if (!job.data) {
    return job.error ? <Failure error={job.error} /> : null;
  }
  return <JobView response={job.data} reload={job.reload} />;
}

function JobView({ response, reload }: { response: JobResponse; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const job = response.job;
  const result = lastResult(t, job);
  return (
    <>
      <PageHead title={job.name} actions={<Actions job={job} reload={reload} />} />
      <Card>
        <CardBody gap={16}>
          <span className="machine-state">
            <JobPill status={job.status} />
            <span className="meta">
              {job.machine_id !== "" && (
                <>
                  <Link to={`/machines/${job.machine_id}`}>{job.machine_name}</Link>
                  <span className="sep" />
                </>
              )}
              <span>
                {t("job.every")} <span className="mono">{formatDuration(t, job.interval_seconds * 1000)}</span>
              </span>
              <span className="sep" />
              <span>
                {t("job.grace")} <span className="mono">{formatDuration(t, job.grace_seconds * 1000)}</span>
              </span>
              <span className="sep" />
              <span>
                {t("job.last_ping")} {lastPing(t, job.last_ping_at, now)}
              </span>
              <span className="sep" />
              <span>{deadlineIn(t, job.next_deadline_at, now)}</span>
              {result && (
                <>
                  <span className="sep" />
                  <span className={`mono result result-${result.tone}`}>{result.text}</span>
                </>
              )}
            </span>
          </span>
        </CardBody>
      </Card>
      <Card>
        <CardHeader title={t("job.ping_url_title")} aside={t("job.ping_url_text")} />
        <CardBody>
          <TokenBox value={response.ping_url} />
          {response.url_local && <Note tone="danger">{t("job.url_local")}</Note>}
          <KeyValue label={t("job.form_finish")} value={response.ping_url} mono />
          <KeyValue label={t("job.form_start")} value={`${response.ping_url}/start`} mono />
          <KeyValue label={t("job.form_code")} value={`${response.ping_url}/{code}`} mono />
        </CardBody>
      </Card>
      <Card>
        <CardHeader title={t("job.snippets_title")} aside={t("job.snippets_text")} />
        <CardBody gap={16}>
          {response.snippets.map((snippet) => (
            <div className="field" key={snippet.key}>
              <div className="kv">
                <span className="field-label">{t(`job.snippet_${snippet.key}`)}</span>
                <CopyButton text={snippet.code} ghost />
              </div>
              <pre className="snippet mono">{snippet.code}</pre>
            </div>
          ))}
        </CardBody>
      </Card>
      <div className="grid-2">
        <Card>
          <CardHeader title={t("job.runs_title")} aside={t("job.runs_text")} />
          {response.runs.length > 0 ? (
            <List>
              {response.runs.map((run) => (
                <RunRow key={run.id} run={run} now={now} />
              ))}
            </List>
          ) : (
            <Empty text={t("job.runs_empty")} />
          )}
        </Card>
        <Card>
          <CardHeader title={t("job.pings_title")} aside={t("job.pings_text")} />
          {response.pings.length > 0 ? (
            <Table>
              <thead>
                <tr>
                  <th>{t("job.col_kind")}</th>
                  <th>{t("job.col_source")}</th>
                  <th>{t("job.col_received")}</th>
                </tr>
              </thead>
              <tbody>
                {response.pings.map((ping) => (
                  <tr key={ping.id}>
                    <td>
                      {t(`job.kind_${ping.kind}`)}
                      {ping.exit_code !== null && (
                        <>
                          {" "}
                          <span className="mono">{ping.exit_code}</span>
                        </>
                      )}
                    </td>
                    <td className="num">
                      {ping.source} · {ping.method}
                    </td>
                    <td className="num">{formatClock(ping.received_at)}</td>
                  </tr>
                ))}
              </tbody>
            </Table>
          ) : (
            <Empty text={t("job.pings_empty")} />
          )}
        </Card>
      </div>
    </>
  );
}

function RunRow({ run, now }: { run: Run; now: number }) {
  const t = useT();
  const when = run.completed_at ?? run.started_at;
  return (
    <div className="row">
      <Dot tone={runTones[run.outcome]} />
      <RowText
        title={
          <>
            {t(`job.outcome_${run.outcome}`)}
            {run.exit_code !== null && (
              <>
                {" · "}
                <span className="mono">{t("job.exit_code", run.exit_code)}</span>
              </>
            )}
            {run.duration_ms !== null && (
              <>
                {" · "}
                <span className="mono">{formatDuration(t, run.duration_ms)}</span>
              </>
            )}
          </>
        }
      >
        {run.started_at !== null && t("job.started_at", formatClock(run.started_at))}
        {run.payload !== "" && (
          <>
            {run.started_at !== null && <br />}
            <span className="mono">{preview(run.payload)}</span>
          </>
        )}
      </RowText>
      <RowWhen>{when !== null ? ago(t, when, now) : ""}</RowWhen>
    </div>
  );
}

// Mettre en pause, reprendre, supprimer après confirmation.
function Actions({ job, reload }: { job: Job; reload: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [error, setError] = useState<unknown>(null);

  const act = (action: "pause" | "resume") => {
    api
      .post(`/api/jobs/${job.id}/actions/${action}`)
      .then(() => {
        reload();
        refresh();
      })
      .catch(setError);
  };
  const remove = () => {
    if (!window.confirm(t("job.delete_confirm", job.name))) {
      return;
    }
    api
      .delete(`/api/jobs/${job.id}`)
      .then(() => {
        refresh();
        void navigate("/taches");
      })
      .catch(setError);
  };

  return (
    <>
      {error !== null && <Failure error={error} />}
      {job.status === "paused" ? (
        <Button icon="refresh" onClick={() => act("resume")}>
          {t("job.resume")}
        </Button>
      ) : (
        <Button icon="pause" onClick={() => act("pause")}>
          {t("job.pause")}
        </Button>
      )}
      <Button variant="danger" icon="x" onClick={remove}>
        {t("job.delete")}
      </Button>
    </>
  );
}
