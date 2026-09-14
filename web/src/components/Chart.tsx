import { useEffect, useRef, useState, type MouseEvent } from "react";

import { useT } from "../i18n/context";
import "./Chart.scss";

export interface ChartPoint {
  at: number;
  value: number;
}

export interface ChartSeries {
  key: string;
  label: string;
  points: ChartPoint[];
}

interface Props {
  series: ChartSeries[];
  from: number;
  to: number;
  // Le pas des points, en millisecondes : deux points plus éloignés que le
  // double font un trou, pas un trait.
  stepMs: number;
  // Le haut de l'échelle ; sans lui, le plus grand point vu.
  max?: number | undefined;
  format: (value: number) => string;
  height?: number;
}

const margin = { top: 12, right: 12, bottom: 22, left: 52 };
const gridLines = 3;
const timeTicks = 4;

// Un graphe d'aire en SVG, dans les jetons : rien d'importé, rien en ligne.
// Les positions sont des attributs, jamais un style, pour rester dans la CSP.
export function Chart({ series, from, to, stepMs, max, format, height = 160 }: Props) {
  const t = useT();
  const holder = useRef<HTMLDivElement>(null);
  const width = useWidth(holder);
  const [hover, setHover] = useState<number | null>(null);

  const plotWidth = Math.max(0, width - margin.left - margin.right);
  const plotHeight = height - margin.top - margin.bottom;
  const top = scaleTop(series, max);
  const x = (at: number) => margin.left + ((at - from) / (to - from)) * plotWidth;
  const y = (value: number) => margin.top + plotHeight - (Math.min(value, top) / top) * plotHeight;
  const hasPoints = series.some((line) => line.points.length > 0);

  const onMove = (event: MouseEvent<SVGSVGElement>) => {
    const box = event.currentTarget.getBoundingClientRect();
    const at = from + ((event.clientX - box.left - margin.left) / plotWidth) * (to - from);
    setHover(Math.min(to, Math.max(from, at)));
  };

  return (
    <div className="chart" ref={holder}>
      {width > 0 && (
        <svg width={width} height={height} onMouseMove={onMove} onMouseLeave={() => setHover(null)} role="img">
          {Array.from({ length: gridLines + 1 }, (_, index) => {
            const value = (top * index) / gridLines;
            return (
              <g key={index}>
                <line className="chart-grid" x1={margin.left} x2={width - margin.right} y1={y(value)} y2={y(value)} />
                <text className="chart-axis" x={margin.left - 8} y={y(value) + 4} textAnchor="end">
                  {format(value)}
                </text>
              </g>
            );
          })}
          {Array.from({ length: timeTicks + 1 }, (_, index) => {
            const at = from + ((to - from) * index) / timeTicks;
            const anchor = index === 0 ? "start" : index === timeTicks ? "end" : "middle";
            return (
              <text key={index} className="chart-axis" x={x(at)} y={height - 6} textAnchor={anchor}>
                {timeLabel(at, to - from)}
              </text>
            );
          })}
          {series.map((line, index) => (
            <g key={line.key} className={`chart-series s${index + 1}`}>
              <path className="chart-area" d={areaPath(line.points, x, y, margin.top + plotHeight, stepMs)} />
              <path className="chart-line" d={linePath(line.points, x, y, stepMs)} />
            </g>
          ))}
          {!hasPoints && (
            <text className="chart-empty" x={margin.left + plotWidth / 2} y={margin.top + plotHeight / 2} textAnchor="middle">
              {t("resource.no_data")}
            </text>
          )}
          {hover !== null && hasPoints && <Cursor series={series} at={hover} x={x} y={y} format={format} width={width} top={margin.top} bottom={margin.top + plotHeight} />}
        </svg>
      )}
    </div>
  );
}

function Cursor({
  series,
  at,
  x,
  y,
  format,
  width,
  top,
  bottom,
}: {
  series: ChartSeries[];
  at: number;
  x: (at: number) => number;
  y: (value: number) => number;
  format: (value: number) => string;
  width: number;
  top: number;
  bottom: number;
}) {
  const nearest: { line: ChartSeries; point: ChartPoint }[] = [];
  for (const line of series) {
    const point = nearestPoint(line.points, at);
    if (point !== null) {
      nearest.push({ line, point });
    }
  }
  const first = nearest[0];
  if (first === undefined) {
    return null;
  }
  const cx = x(first.point.at);
  const onRight = cx > width / 2;
  const label = nearest.map((hit) => `${hit.line.label} ${format(hit.point.value)}`).join(" · ");
  return (
    <g className="chart-cursor">
      <line x1={cx} x2={cx} y1={top} y2={bottom} />
      {nearest.map((hit) => (
        <circle key={hit.line.key} cx={x(hit.point.at)} cy={y(hit.point.value)} r="3" />
      ))}
      <text x={onRight ? cx - 8 : cx + 8} y={top + 10} textAnchor={onRight ? "end" : "start"}>
        {`${clock(first.point.at)} · ${label}`}
      </text>
    </g>
  );
}

// La largeur du conteneur, suivie : le SVG se dessine en pixels.
function useWidth(holder: React.RefObject<HTMLDivElement | null>): number {
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const element = holder.current;
    if (!element) {
      return;
    }
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setWidth(Math.floor(entry.contentRect.width));
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [holder]);
  return width;
}

// Le haut de l'échelle : le maximum donné, sinon le plus grand point vu
// avec une marge, et jamais zéro.
function scaleTop(series: ChartSeries[], max: number | undefined): number {
  if (max !== undefined && max > 0) {
    return max;
  }
  let seen = 0;
  for (const line of series) {
    for (const point of line.points) {
      seen = Math.max(seen, point.value);
    }
  }
  return seen > 0 ? niceCeiling(seen * 1.15) : 1;
}

// Arrondit vers le haut à un chiffre lisible : 1, 2, 5 × 10ⁿ.
function niceCeiling(value: number): number {
  const power = 10 ** Math.floor(Math.log10(value));
  for (const factor of [1, 2, 5, 10]) {
    if (factor * power >= value) {
      return factor * power;
    }
  }
  return 10 * power;
}

// Le trait s'interrompt quand deux points sont plus éloignés que deux pas.
function linePath(points: ChartPoint[], x: (at: number) => number, y: (value: number) => number, stepMs: number): string {
  let path = "";
  let previous: ChartPoint | null = null;
  for (const point of points) {
    const gap = previous !== null && point.at - previous.at > 2 * stepMs;
    path += `${previous === null || gap ? "M" : "L"}${x(point.at).toFixed(1)} ${y(point.value).toFixed(1)}`;
    previous = point;
  }
  return path;
}

// L'aire ferme chaque segment continu vers le bas du graphe.
function areaPath(points: ChartPoint[], x: (at: number) => number, y: (value: number) => number, floor: number, stepMs: number): string {
  let path = "";
  let start: ChartPoint | null = null;
  let previous: ChartPoint | null = null;
  const close = () => {
    if (previous !== null && start !== null) {
      path += `L${x(previous.at).toFixed(1)} ${floor}L${x(start.at).toFixed(1)} ${floor}Z`;
    }
  };
  for (const point of points) {
    if (previous === null || point.at - previous.at > 2 * stepMs) {
      close();
      start = point;
      path += `M${x(point.at).toFixed(1)} ${y(point.value).toFixed(1)}`;
    } else {
      path += `L${x(point.at).toFixed(1)} ${y(point.value).toFixed(1)}`;
    }
    previous = point;
  }
  close();
  return path;
}

function nearestPoint(points: ChartPoint[], at: number): ChartPoint | null {
  let best: ChartPoint | null = null;
  for (const point of points) {
    if (best === null || Math.abs(point.at - at) < Math.abs(best.at - at)) {
      best = point;
    }
  }
  return best;
}

const pad = (value: number) => String(value).padStart(2, "0");

function clock(at: number): string {
  const date = new Date(at);
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

// L'axe du temps en heure locale : l'heure sous 24 h, le jour au-delà.
function timeLabel(at: number, spanMs: number): string {
  const date = new Date(at);
  if (spanMs <= 24 * 60 * 60 * 1000) {
    return clock(at);
  }
  return `${pad(date.getDate())}/${pad(date.getMonth() + 1)}`;
}
