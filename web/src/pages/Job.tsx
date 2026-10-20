import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { deleteJob, pauseJob, regenerateJobToken, resumeJob, useJob, type Job, type JobResponse, type Ping, type Run } from "../api/jobs";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "../components/Card";
import { CopyButton, TokenBox } from "../components/Copy";
import { Disclosure, FoldSection } from "../components/Disclosure";
import { Failure } from "../components/Failure";
import { Loaded } from "../components/Loaded";
import { PageHead } from "../components/PageHead";
import { Dot, JobPill } from "../components/Pill";
import { RunStrip } from "../components/RunStrip";
import { List, RowText, RowWhen, Table } from "../components/Table";
import { Verdict } from "../components/Verdict";
import { useNow } from "../hooks/useNow";
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { awaitsFirstPing, jobFact, jobTones, lastResult, lastSuccessText, preview, runsShownFirst, runTones } from "../lib/jobs";
import { ago, deadlineIn, everyOnce, formatClock, formatDuration, lastPing } from "../lib/time";

// La page d'une tâche : le verdict, les dernières exécutions, puis les
// pings bruts et l'installation, repliés. Tant qu'aucun ping n'est arrivé,
// l'installation passe devant, ouverte : c'est ce qu'il reste à faire.
export function JobPage() {
  const { id = "" } = useParams();
  const job = useJob(id);
  return <Loaded resource={job}>{(data) => <JobView key={data.job.id} response={data} reload={job.reload} />}</Loaded>;
}

function JobView({ response, reload }: { response: JobResponse; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const job = response.job;
  const result = lastResult(t, job);
  // Décidé à l'ouverture de la page : le premier ping ne déplace pas le bloc
  // sous les yeux de l'opérateur qui est en train de le recopier.
  const [installsFirst] = useState(awaitsFirstPing(job));
  return (
    <>
      <PageHead title={job.name} actions={<Actions job={job} reload={reload} />} />
      <Verdict
        tone={jobTones[job.status]}
        pill={<JobPill status={job.status} />}
        fact={jobFact(t, job, response.runs, now)}
        context={lastSuccessText(t, job, response.runs, now)}
      >
        {job.machine_id !== "" && (
          <>
            <Link to={`/machines/${job.machine_id}`}>{job.machine_name}</Link>
            <span className="sep" />
          </>
        )}
        <span>
          {everyOnce(t, job.interval_seconds * 1000) ?? (
            <>
              {t("time.every")} <span className="mono">{formatDuration(t, job.interval_seconds * 1000)}</span>
            </>
          )}
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
      </Verdict>
      {installsFirst && <Installation response={response} reload={reload} initiallyOpen={true} />}
      <Runs runs={response.runs} now={now} />
      <Pings pings={response.pings} />
      {!installsFirst && <Installation response={response} reload={reload} initiallyOpen={false} />}
    </>
  );
}

// Les exécutions : la frise dit la tendance, la liste les dernières ; le
// reste de l'historique se déplie.
function Runs({ runs, now }: { runs: Run[]; now: number }) {
  const t = useT();
  const [showsAll, setShowsAll] = useState(false);
  const shown = showsAll ? runs : runs.slice(0, runsShownFirst);
  return (
    <Card>
      <CardHeader title={t("job.runs_title")} aside={runs.length > 0 ? <RunStrip runs={runs} /> : undefined} />
      {runs.length > 0 ? (
        <List>
          {shown.map((run) => (
            <RunRow key={run.id} run={run} now={now} />
          ))}
        </List>
      ) : (
        <Empty text={t("job.runs_empty")} />
      )}
      {runs.length > runsShownFirst && (
        <div className="card-foot">
          <Button variant="ghost" small onClick={() => setShowsAll(!showsAll)}>
            {showsAll ? t("job.runs_show_fewer", runsShownFirst) : t("job.runs_show_all", runs.length)}
          </Button>
        </div>
      )}
    </Card>
  );
}

// Les pings bruts : le détail qu'on ouvre pour comprendre une exécution.
function Pings({ pings }: { pings: Ping[] }) {
  const t = useT();
  return (
    <Disclosure title={t("job.pings_title")} aside={t("job.pings_text")} initiallyOpen={false}>
      {pings.length > 0 ? (
        <Table>
          <thead>
            <tr>
              <th>{t("job.col_kind")}</th>
              <th>{t("job.col_source")}</th>
              <th>{t("job.col_received")}</th>
            </tr>
          </thead>
          <tbody>
            {pings.map((ping) => (
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
    </Disclosure>
  );
}

// L'URL de ping et les extraits à coller : ouverts tant que la tâche reste
// à brancher, repliés ensuite. Le jeton se régénère d'ici, après
// confirmation : l'ancienne URL cesse aussitôt de répondre.
function Installation({ response, reload, initiallyOpen }: { response: JobResponse; reload: () => void; initiallyOpen: boolean }) {
  const t = useT();
  const action = useAction();
  const regenerate = () => {
    action.run(() => regenerateJobToken(response.job.id), {
      confirm: t("job.regenerate_confirm", response.job.name),
      onDone: reload,
    });
  };
  return (
    <Disclosure title={t("job.install_title")} aside={t("job.install_text")} initiallyOpen={initiallyOpen}>
      <CardBody gap={24}>
        <FoldSection title={t("job.ping_url_title")} text={t("job.ping_url_text")}>
          <TokenBox value={response.ping_url} />
          {response.url_local && <Note tone="danger">{t("job.url_local")}</Note>}
          <Failure error={action.error} />
          <div className="cluster">
            <Button variant="ghost" icon="refresh" onClick={regenerate}>
              {t("job.regenerate")}
            </Button>
          </div>
          <KeyValue label={t("job.form_finish")} value={response.ping_url} mono />
          <KeyValue label={t("job.form_start")} value={`${response.ping_url}/start`} mono />
          <KeyValue label={t("job.form_code")} value={`${response.ping_url}/{code}`} mono />
        </FoldSection>
        <FoldSection title={t("job.snippets_title")} text={t("job.snippets_text")}>
          {response.snippets.map((snippet) => (
            <div className="field" key={snippet.key}>
              <div className="kv">
                <span className="field-label">{t(`job.snippet_${snippet.key}`)}</span>
                <CopyButton text={snippet.code} ghost />
              </div>
              <pre className="snippet mono">{snippet.code}</pre>
            </div>
          ))}
        </FoldSection>
      </CardBody>
    </Disclosure>
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

// Modifier, mettre en pause, reprendre, supprimer après confirmation.
function Actions({ job, reload }: { job: Job; reload: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const action = useAction();

  const pause = () => {
    action.run(() => pauseJob(job.id), { topic: "all", onDone: reload });
  };
  const resume = () => {
    action.run(() => resumeJob(job.id), { topic: "all", onDone: reload });
  };
  const remove = () => {
    action.run(() => deleteJob(job.id), {
      confirm: t("job.delete_confirm", job.name),
      topic: "all",
      onDone: () => void navigate("/taches"),
    });
  };

  return (
    <>
      <Failure error={action.error} />
      <Button icon="settings" to={`/taches/${job.id}/modifier`}>
        {t("job.edit")}
      </Button>
      {job.status === "paused" ? (
        <Button icon="refresh" onClick={resume}>
          {t("job.resume")}
        </Button>
      ) : (
        <Button icon="pause" onClick={pause}>
          {t("job.pause")}
        </Button>
      )}
      <Button variant="danger" icon="x" onClick={remove}>
        {t("job.delete")}
      </Button>
    </>
  );
}
