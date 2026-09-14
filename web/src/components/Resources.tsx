import { useState } from "react";

import type { Current, Disk, HistoryResponse, Reading, WindowName } from "../api/types";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useI18n } from "../i18n/context";
import { fullestFirst } from "../lib/disks";
import { ago } from "../lib/time";
import { bytesIn, bytesQuantity, formatBytes, formatDecimal, formatRate, percentOf, rateQuantity } from "../lib/units";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, Meter, Stat } from "./Card";
import { Chart, type ChartSeries } from "./Chart";
import { Dot } from "./Pill";
import "./Resources.scss";

// Une jauge passe en attention à 85 %, en danger à 95 % : le seuil de la
// planche « Machine », en attendant que la feature alertes le règle.
const warnAt = 85;
const dangerAt = 95;

export function meterTone(percent: number): "ok" | "warn" | "danger" | undefined {
  if (percent >= dangerAt) {
    return "danger";
  }
  if (percent >= warnAt) {
    return "warn";
  }
  return undefined;
}

// Les quatre chiffres clés d'une machine : processeur, mémoire, disque,
// réseau. Sans mesure fraîche, un tiret et la raison, jamais un zéro.
export function ResourceStats({ current }: { current: Current | null }) {
  const { t, language } = useI18n();
  const now = useNow();
  const sample = current?.available ? current.sample : null;
  const foot = unavailableFoot(t, now, current);
  const memory = sample ? bytesQuantity(t, language, sample.mem_total) : null;
  const disk = sample ? bytesQuantity(t, language, sample.disk_total) : null;
  const rate = sample ? rateQuantity(t, language, sample.net_rx_per_second + sample.net_tx_per_second) : null;
  const memPercent = sample ? percentOf(sample.mem_used, sample.mem_total) : 0;
  const diskPercent = sample ? percentOf(sample.disk_used, sample.disk_total) : 0;
  const volumes = sample?.disks ?? [];
  return (
    <div className="grid-4">
      <Stat
        icon="cpu"
        label={t("resource.cpu")}
        value={sample ? Math.round(sample.cpu_percent) : "–"}
        unit="%"
        meter={<Meter percent={sample ? sample.cpu_percent : 0} tone={sample ? meterTone(sample.cpu_percent) : undefined} />}
        foot={sample ? t("resource.cpu_foot", sample.cpu_cores, formatDecimal(language, sample.load_1)) : foot}
      />
      <Stat
        icon="memory"
        label={t("resource.memory")}
        value={sample && memory ? bytesIn(language, sample.mem_used, sample.mem_total) : "–"}
        unit={memory ? `${memory.unit} / ${memory.value}` : undefined}
        meter={<Meter percent={memPercent} tone={sample ? meterTone(memPercent) : undefined} />}
        foot={sample ? t("resource.mem_foot", memPercent, formatBytes(t, language, sample.swap_used), formatBytes(t, language, sample.swap_total)) : foot}
      />
      <Stat
        icon="disk"
        label={t("resource.disk")}
        value={sample && disk ? bytesIn(language, sample.disk_used, sample.disk_total) : "–"}
        unit={disk ? `${disk.unit} / ${disk.value}` : undefined}
        meter={volumes.length > 1 ? <DiskVolumes disks={volumes} /> : <Meter percent={diskPercent} tone={sample ? meterTone(diskPercent) : undefined} />}
        foot={diskFoot(t, sample, volumes.length, diskPercent, foot)}
      />
      <Stat
        icon="network"
        label={t("resource.network")}
        value={rate ? rate.value : "–"}
        unit={rate?.unit}
        foot={sample ? t("resource.net_foot", formatRate(t, language, sample.net_rx_per_second), formatRate(t, language, sample.net_tx_per_second)) : foot}
      />
    </div>
  );
}

// Une jauge par volume, la plus pleine en tête, nommée par son point de
// montage ; le périphérique et les octets sont dans l'info-bulle.
function DiskVolumes({ disks }: { disks: Disk[] }) {
  const { t, language } = useI18n();
  return (
    <div className="disk-volumes">
      {fullestFirst(disks).map((disk) => {
        const percent = percentOf(disk.used, disk.total);
        const detail = `${disk.device} · ${formatBytes(t, language, disk.used)} / ${formatBytes(t, language, disk.total)}`;
        return (
          <div className="meter-row" key={disk.mount_point} title={detail}>
            <div className="kv">
              <span className="k mono">{disk.mount_point}</span>
              <span className="mono">{percent} %</span>
            </div>
            <Meter percent={percent} tone={meterTone(percent)} />
          </div>
        );
      })}
    </div>
  );
}

// Un seul volume : son pourcentage ; plusieurs : leur nombre et le total.
function diskFoot(t: ReturnType<typeof useI18n>["t"], sample: Reading | null, volumes: number, percent: number, unavailable: string): string {
  if (!sample) {
    return unavailable;
  }
  if (volumes > 1) {
    return t("resource.disks_foot", volumes, percent);
  }
  return t("resource.disk_foot", percent);
}

// Pourquoi il n'y a pas de chiffre : jamais mesuré, ou mesuré trop tôt.
function unavailableFoot(t: ReturnType<typeof useI18n>["t"], now: number, current: Current | null): string {
  if (current?.sample) {
    return t("resource.last_measure", ago(t, current.sample.sampled_at, now));
  }
  return t("resource.unavailable_text");
}

// Les trois jauges d'une carte de machine, sur la vue d'ensemble.
export function MachineMeters({ current }: { current: Current | undefined }) {
  const { t } = useI18n();
  const sample = current?.available ? current.sample : null;
  const rows: { key: string; percent: number }[] = sample
    ? [
        { key: "resource.cpu", percent: Math.round(sample.cpu_percent) },
        { key: "resource.memory", percent: percentOf(sample.mem_used, sample.mem_total) },
        { key: "resource.disk", percent: percentOf(sample.disk_used, sample.disk_total) },
      ]
    : [
        { key: "resource.cpu", percent: 0 },
        { key: "resource.memory", percent: 0 },
        { key: "resource.disk", percent: 0 },
      ];
  return (
    <div className="meters">
      {rows.map((row) => (
        <div className="meter-row" key={row.key}>
          <div className="kv">
            <span className="k">{t(row.key)}</span>
            <span className={sample ? "mono" : "muted"}>{sample ? `${row.percent} %` : t("resource.unavailable")}</span>
          </div>
          <Meter percent={row.percent} tone={sample ? meterTone(row.percent) : undefined} />
        </div>
      ))}
    </div>
  );
}

const windows: WindowName[] = ["1h", "24h", "7d", "30d", "90d"];

// L'historique d'une machine : une fenêtre, quatre graphes.
export function ResourceHistory({ machineID }: { machineID: string }) {
  const { t, language } = useI18n();
  const [window, setWindow] = useState<WindowName>("1h");
  const history = useResource<HistoryResponse>(`/api/machines/${machineID}/resources/history?window=${window}`);
  const now = useNow(10_000);
  const data = history.data;
  const spanMs = (data?.span_seconds ?? 3600) * 1000;
  const stepMs = (data?.step_seconds ?? 10) * 1000;
  const points = data?.points ?? [];
  const to = now;
  const from = to - spanMs;
  const memTotal = Math.max(0, ...points.map((point) => point.mem_total));
  const diskTotal = Math.max(0, ...points.map((point) => point.disk_total));
  const percent = (value: number) => `${Math.round(value)} %`;
  const bytes = (value: number) => formatBytes(t, language, value);
  const rate = (value: number) => formatRate(t, language, value);
  return (
    <Card>
      <CardHeader
        title={t("resource.history_title")}
        aside={
          <span className="segmented">
            {windows.map((name) => (
              <Button key={name} small variant={name === window ? "primary" : "ghost"} onClick={() => setWindow(name)}>
                {t(`resource.window_${name}`)}
              </Button>
            ))}
          </span>
        }
      />
      <CardBody gap={16}>
        <div className="grid-2 gap-24">
          <ChartBlock title={t("resource.cpu")} series={[series("cpu", t("resource.used"), points, (point) => point.cpu_percent)]} from={from} to={to} stepMs={stepMs} max={100} format={percent} />
          <ChartBlock title={t("resource.memory")} series={[series("mem", t("resource.used"), points, (point) => point.mem_used)]} from={from} to={to} stepMs={stepMs} max={memTotal} format={bytes} />
          <ChartBlock
            title={t("resource.network")}
            series={[
              series("rx", t("resource.received"), points, (point) => point.net_rx_per_second),
              series("tx", t("resource.sent"), points, (point) => point.net_tx_per_second),
            ]}
            from={from}
            to={to}
            stepMs={stepMs}
            format={rate}
          />
          <ChartBlock title={t("resource.disk")} series={[series("disk", t("resource.used"), points, (point) => point.disk_used)]} from={from} to={to} stepMs={stepMs} max={diskTotal} format={bytes} />
        </div>
        <span className="secondary">{t("resource.history_text")}</span>
      </CardBody>
    </Card>
  );
}

function series(key: string, label: string, points: Reading[], pick: (point: Reading) => number): ChartSeries {
  return { key, label, points: points.map((point) => ({ at: Date.parse(point.sampled_at), value: pick(point) })) };
}

function ChartBlock({
  title,
  series,
  from,
  to,
  stepMs,
  max,
  format,
}: {
  title: string;
  series: ChartSeries[];
  from: number;
  to: number;
  stepMs: number;
  max?: number | undefined;
  format: (value: number) => string;
}) {
  return (
    <div className="chart-block">
      <div className="chart-head">
        <span className="side-label">{title}</span>
        {series.length > 1 && (
          <span className="chart-legend">
            {series.map((line, index) => (
              <span key={line.key}>
                <Dot tone={index === 0 ? "accent" : "ok"} />
                {line.label}
              </span>
            ))}
          </span>
        )}
      </div>
      <Chart series={series} from={from} to={to} stepMs={stepMs} max={max} format={format} />
    </div>
  );
}
