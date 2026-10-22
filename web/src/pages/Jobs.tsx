import { Link } from "react-router";

import { useJobs } from "../api/jobs";
import { Button } from "../components/Button";
import { Card, EmptyCard } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { JobPill, Pill } from "../components/Pill";
import { Subject, Table } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { attentionStatuses, jobsSubtitle, lastResult } from "../lib/jobs";
import { formatDuration, lastPing } from "../lib/time";

export function Jobs() {
  const t = useT();
  const now = useNow();
  const jobs = useJobs();
  const list = jobs.data?.jobs ?? [];
  const attention = list.filter((job) => attentionStatuses.has(job.status)).length;
  return (
    <>
      <PageHead
        title={t("nav.jobs")}
        subtitle={jobs.data ? jobsSubtitle(t, list.length, attention) : ""}
        actions={
          <Button variant="primary" icon="plus" to="/taches/nouvelle">
            {t("job.add")}
          </Button>
        }
      />
      {jobs.error && <Failure error={jobs.error} />}
      {jobs.data && list.length === 0 && (
        <EmptyCard icon="clock" title={t("jobs.empty_title")} text={t("jobs.empty_text")} />
      )}
      {jobs.data && list.length > 0 && (
        <Card className="scroll-x">
          <Table>
            <thead>
              <tr>
                <th>{t("jobs.col_job")}</th>
                <th className="wide-only">{t("jobs.col_machine")}</th>
                <th className="wide-only">{t("jobs.col_interval")}</th>
                <th>{t("jobs.col_state")}</th>
                <th>{t("jobs.col_last_ping")}</th>
                <th className="th-end wide-only">{t("jobs.col_result")}</th>
              </tr>
            </thead>
            <tbody>
              {list.map((job) => {
                const result = lastResult(t, job);
                return (
                  <tr key={job.id}>
                    <td>
                      <Link className="svc" to={`/taches/${job.id}`}>
                        <Subject icon="clock">{job.name}</Subject>
                      </Link>
                    </td>
                    <td className="wide-only">{job.machine_id !== "" ? <Link to={`/machines/${job.machine_id}`}>{job.machine_name}</Link> : <span className="muted">—</span>}</td>
                    <td className="num wide-only">{formatDuration(t, job.interval_seconds * 1000)}</td>
                    <td>
                      <JobPill status={job.status} />
                    </td>
                    <td className="secondary nowrap">{lastPing(t, job.last_ping_at, now)}</td>
                    <td className="num td-end wide-only">{result && (
                        <Pill tone={result.tone} dot={result.tone !== "neutral"} small>
                          {result.text}
                        </Pill>
                      )}</td>
                  </tr>
                );
              })}
            </tbody>
          </Table>
        </Card>
      )}
    </>
  );
}
