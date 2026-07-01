'use client';

import { useEffect, useRef } from 'react';
import {
  AreaSeries,
  ColorType,
  createChart,
  HistogramSeries,
  type HistogramData,
  type IChartApi,
  type ISeriesApi,
  type LineData,
  type Time,
} from 'lightweight-charts';

import { localTickMarkFormatter, localCrosshairTimeFormatter } from './localTime';

export type LinePoint = {
  /** Unix epoch seconds */
  time: number;
  value: number;
  /** Optional per-bar volume — renders a histogram under the line. */
  volume?: number;
};

export type LineChartProps = {
  data: LinePoint[];
  height?: number;
  className?: string;
  /**
   * Tone the line green/red based on the overall trend. Default
   * derives from first→last sign; pass an explicit boolean to
   * override (e.g. when a parent is animating between datasets and
   * wants to keep the line stable).
   */
  positive?: boolean;
  /**
   * Text alternative for the canvas-rendered chart (WCAG 1.1.1).
   * See [CandleChart] — same rationale.
   */
  ariaLabel?: string;
  /**
   * When false, force the area-fill off and render a thin line only
   * (used for dense count-series like throughput). Default true.
   */
  area?: boolean;
  /**
   * Show time-of-day on the x-axis (for intraday/hourly series).
   * Default false (daily series read better without it).
   */
  timeVisible?: boolean;
  /**
   * When set, render a crosshair-following legend showing the hovered
   * point's line value (and volume, when present). Makes the volume
   * histogram hoverable — lightweight-charts only surfaces the
   * active-scale value on the axis otherwise.
   */
  legend?: {
    valueLabel: string;
    volumeLabel?: string;
    formatValue?: (n: number) => string;
    formatVolume?: (n: number) => string;
  };
};

/**
 * LineChart — TradingView Lightweight Charts wrapper for scalar
 * (time, value) series, with an OPTIONAL volume histogram underneath
 * (rendered when any point carries a `volume`). Companion to
 * [CandleChart] — same lifecycle, theme, resize, and bottom-pinned
 * volume overlay scale. Use this for line/area price series or any
 * metric trend; use CandleChart when you have OHLC.
 */
export function LineChart({
  data,
  height = 320,
  className,
  positive,
  ariaLabel,
  area = true,
  timeVisible = false,
  legend,
}: LineChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<'Area'> | null>(null);
  const volumeRef = useRef<ISeriesApi<'Histogram'> | null>(null);
  const legendRef = useRef<HTMLDivElement>(null);
  // Hold the latest legend config in a ref so the (once-installed)
  // crosshair handler reads current formatters without re-creating the
  // chart on every render (the prop is usually an inline literal).
  const legendCfgRef = useRef(legend);
  legendCfgRef.current = legend;
  const legendEnabled = !!legend;

  // Resolve trend tone — first vs last value when the caller doesn't
  // specify. Renders green when the series is up, red when down,
  // neutral grey-blue when flat or empty.
  const isUp = positive ?? trendUp(data);
  const hasVolume = data.some((p) => p.volume != null && Number.isFinite(p.volume));

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const chart = createChart(container, {
      layout: {
        background: { type: ColorType.Solid, color: 'transparent' },
        textColor: '#64748b',
        fontFamily: 'var(--font-sans)',
        fontSize: 11,
      },
      grid: {
        horzLines: { color: 'rgba(148, 163, 184, 0.15)' },
        vertLines: { color: 'rgba(148, 163, 184, 0.10)' },
      },
      timeScale: {
        timeVisible,
        secondsVisible: false,
        borderColor: 'rgba(148, 163, 184, 0.25)',
        // Intraday charts (timeVisible) label the axis in the viewer's
        // local timezone — see ./localTime. Daily charts keep the
        // default UTC date labels (a daily bucket is a UTC calendar day).
        ...(timeVisible ? { tickMarkFormatter: localTickMarkFormatter } : {}),
      },
      ...(timeVisible
        ? { localization: { timeFormatter: localCrosshairTimeFormatter } }
        : {}),
      rightPriceScale: {
        borderColor: 'rgba(148, 163, 184, 0.25)',
        // Leave room at the bottom for the volume histogram when present.
        scaleMargins: hasVolume ? { top: 0.08, bottom: 0.26 } : { top: 0.1, bottom: 0.1 },
      },
      crosshair: {
        mode: 1,
      },
      width: container.clientWidth,
      height,
    });
    chartRef.current = chart;

    const lineColor = isUp ? '#059669' : '#e11d48';
    const fillColor = area
      ? isUp
        ? 'rgba(16, 185, 129, 0.15)'
        : 'rgba(244, 63, 94, 0.15)'
      : 'rgba(0,0,0,0)';
    const series = chart.addSeries(AreaSeries, {
      lineColor,
      topColor: fillColor,
      bottomColor: 'rgba(0,0,0,0)',
      lineWidth: 2,
      priceLineVisible: false,
    });
    seriesRef.current = series;

    if (hasVolume) {
      const volume = chart.addSeries(HistogramSeries, {
        priceFormat: { type: 'volume' },
        priceScaleId: 'volume',
        priceLineVisible: false,
        lastValueVisible: false,
      });
      chart.priceScale('volume').applyOptions({
        scaleMargins: { top: 0.8, bottom: 0 },
      });
      volumeRef.current = volume;
    }

    if (legendEnabled) {
      chart.subscribeCrosshairMove((param) => {
        const el = legendRef.current;
        const cfg = legendCfgRef.current;
        if (!el || !cfg) return;
        if (param.time == null || !param.point || param.point.x < 0 || param.point.y < 0) {
          el.style.opacity = '0';
          return;
        }
        const lv = seriesRef.current ? param.seriesData.get(seriesRef.current) : undefined;
        const vv = volumeRef.current ? param.seriesData.get(volumeRef.current) : undefined;
        const valNum = lv && 'value' in lv ? (lv as { value: number }).value : null;
        const volNum = vv && 'value' in vv ? (vv as { value: number }).value : null;
        const parts: string[] = [];
        if (valNum != null) {
          parts.push(`${cfg.valueLabel}: ${cfg.formatValue ? cfg.formatValue(valNum) : valNum.toLocaleString()}`);
        }
        if (volNum != null && cfg.volumeLabel) {
          parts.push(`${cfg.volumeLabel}: ${cfg.formatVolume ? cfg.formatVolume(volNum) : volNum.toLocaleString()}`);
        }
        el.textContent = parts.join('   ·   ');
        el.style.opacity = parts.length ? '1' : '0';
      });
    }

    const ro = new ResizeObserver((entries) => {
      for (const e of entries) {
        chart.applyOptions({ width: e.contentRect.width });
      }
    });
    ro.observe(container);

    return () => {
      ro.disconnect();
      chart.remove();
      chartRef.current = null;
      seriesRef.current = null;
      volumeRef.current = null;
    };
    // height / isUp / hasVolume / area / timeVisible drive chart
    // re-creation; data updates are pushed via setData in the second
    // effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [height, isUp, hasVolume, area, timeVisible, legendEnabled]);

  useEffect(() => {
    seriesRef.current?.setData(toSeries(data));
    volumeRef.current?.setData(toVolume(data));
    chartRef.current?.timeScale().fitContent();
  }, [data]);

  const chartDiv = (
    <div
      ref={containerRef}
      className={className}
      style={{ width: '100%', height }}
      role="img"
      aria-label={
        ariaLabel ??
        `Line chart${data.length ? ` with ${data.length} points` : ''}`
      }
    />
  );

  // Non-legend usages stay byte-identical (no wrapper). Legend usages
  // get a relative wrapper with the crosshair-following legend overlay.
  if (!legend) return chartDiv;
  return (
    <div className="relative" style={{ width: '100%', height }}>
      {chartDiv}
      <div
        ref={legendRef}
        className="pointer-events-none absolute left-2 top-2 rounded-sm border border-line bg-surface/90 px-2 py-1 font-mono text-[11px] text-ink-body opacity-0 shadow-card transition-opacity"
      />
    </div>
  );
}

function toSeries(points: LinePoint[]): LineData<Time>[] {
  return points.map((p) => ({
    time: p.time as Time,
    value: p.value,
  }));
}

// Volume bars, tinted by the bar-over-bar direction of the value
// series (green when the point rose vs the previous, red when it
// fell) at low opacity so they read as context, not foreground.
function toVolume(points: LinePoint[]): HistogramData<Time>[] {
  const out: HistogramData<Time>[] = [];
  for (let i = 0; i < points.length; i++) {
    const p = points[i];
    if (p.volume == null || !Number.isFinite(p.volume)) continue;
    const rising = i === 0 ? true : p.value >= points[i - 1].value;
    out.push({
      time: p.time as Time,
      value: p.volume,
      color: rising ? 'rgba(22, 163, 74, 0.45)' : 'rgba(220, 38, 38, 0.45)',
    });
  }
  return out;
}

function trendUp(points: LinePoint[]): boolean {
  if (points.length < 2) return true;
  return points[points.length - 1].value >= points[0].value;
}
