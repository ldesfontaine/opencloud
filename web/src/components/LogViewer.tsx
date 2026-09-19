import { useCallback, useEffect, useRef, useState } from "react";

import { api, ApiError } from "../api/client";
import type { LogLine, LogsResponse } from "../api/types";
import { useT } from "../i18n/context";
import { formatClock } from "../lib/time";
import { Button } from "./Button";
import { Failure } from "./Failure";
import { Pill } from "./Pill";
import "./LogViewer.scss";

// Le navigateur garde ce nombre de lignes ; au-delà il coupe en tête.
const maxLines = 1200;
const trimTo = 800;
const tail = 100;

type Status = "connecting" | "live" | "fallback" | "ended" | "error";

interface Shown extends LogLine {
  key: number;
}

// La visionneuse : le direct par SSE, le repli sur le tirage unique si le
// direct échoue avant la première ligne, l'autodéfilement tant que
// l'opérateur reste en bas.
export function LogViewer({ serviceID }: { serviceID: string }) {
  const t = useT();
  const [lines, setLines] = useState<Shown[]>([]);
  const [status, setStatus] = useState<Status>("connecting");
  const [error, setError] = useState<unknown>(null);
  const [following, setFollowing] = useState(true);
  const nextKey = useRef(1);
  const stickToBottom = useRef(true);
  const pane = useRef<HTMLDivElement>(null);

  const append = useCallback((added: LogLine[]) => {
    setLines((current) => {
      const merged = current.concat(added.map((line) => ({ ...line, key: nextKey.current++ })));
      return merged.length > maxLines ? merged.slice(-trimTo) : merged;
    });
  }, []);

  const fetchOnce = useCallback(() => {
    setStatus("connecting");
    api
      .get<LogsResponse>(`/api/services/${serviceID}/logs?tail=${tail}`)
      .then((response) => {
        setLines(response.lines.map((line) => ({ ...line, key: nextKey.current++ })));
        setStatus("fallback");
      })
      .catch((failure: unknown) => {
        setError(failure instanceof ApiError ? failure : new ApiError(0, "internal"));
        setStatus("error");
      });
  }, [serviceID]);

  useEffect(() => {
    if (!following) {
      return;
    }
    setLines([]);
    setError(null);
    setStatus("connecting");
    let received = false;
    const source = new EventSource(`/api/services/${serviceID}/logs/stream?tail=${tail}`);
    source.addEventListener("line", (event: MessageEvent<string>) => {
      received = true;
      setStatus("live");
      append([JSON.parse(event.data) as LogLine]);
    });
    source.addEventListener("end", (event: MessageEvent<string>) => {
      source.close();
      const payload = JSON.parse(event.data) as { error?: string };
      if (payload.error) {
        setError(new ApiError(502, payload.error));
        setStatus("error");
      } else {
        setStatus("ended");
      }
    });
    source.onerror = () => {
      source.close();
      if (!received) {
        fetchOnce();
      } else {
        setStatus("ended");
      }
    };
    return () => source.close();
  }, [serviceID, following, append, fetchOnce]);

  useEffect(() => {
    const element = pane.current;
    if (element && stickToBottom.current) {
      element.scrollTop = element.scrollHeight;
    }
  }, [lines]);

  const onScroll = () => {
    const element = pane.current;
    if (element) {
      stickToBottom.current = element.scrollHeight - element.scrollTop - element.clientHeight < 24;
    }
  };

  return (
    <div className="logs">
      <div className="logs-bar">
        <LogStatus status={status} />
        {following ? (
          <Button small icon="pause" onClick={() => setFollowing(false)}>
            {t("service.logs_stop")}
          </Button>
        ) : (
          <Button small icon="refresh" onClick={() => setFollowing(true)}>
            {t("service.logs_follow")}
          </Button>
        )}
      </div>
      {error !== null && <Failure error={error} />}
      <div className="logs-pane mono" ref={pane} onScroll={onScroll}>
        {lines.length === 0 && status !== "connecting" && <span className="muted">{t("service.logs_empty")}</span>}
        {lines.map((line) => (
          <div className={line.stream === "stderr" ? "log-line stderr" : "log-line"} key={line.key}>
            {line.at !== null && <span className="log-at">{formatClock(line.at)}</span>}
            <span className="log-text">{line.text}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function LogStatus({ status }: { status: Status }) {
  const t = useT();
  switch (status) {
    case "live":
      return <Pill tone="ok">{t("service.logs_live")}</Pill>;
    case "fallback":
      return <Pill tone="neutral">{t("service.logs_fallback")}</Pill>;
    case "ended":
      return <Pill tone="neutral">{t("service.logs_paused")}</Pill>;
    case "error":
      return <Pill tone="danger">{t("service.logs_paused")}</Pill>;
    default:
      return (
        <Pill tone="accent" dot={false}>
          {t("service.logs_connecting")}
        </Pill>
      );
  }
}
