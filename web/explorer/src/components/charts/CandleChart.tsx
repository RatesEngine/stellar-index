'use client';

import { useEffect, useRef } from 'react';
import {
  CandlestickSeries,
  ColorType,
  createChart,
  HistogramSeries,
  type CandlestickData,
  type HistogramData,
  type IChartApi,
  type ISeriesApi,
  type Time,
} from 'lightweight-charts';

import { localTickMarkFormatter, localCrosshairTimeFormatter } from './localTime';

export type CandlePoint = {
  /** Unix epoch seconds */
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  /** Optional per-bar volume — renders a histogram under the candles. */
  volume?: number;
};

export type CandleChartProps = {
  data: CandlePoint[];
  height?: number;
  className?: string;
  /**
   * Text alternative for the canvas-rendered chart (WCAG 1.1.1).
   * lightweight-charts paints to a <canvas> with no DOM text, so
   * screen readers get nothing without this. Callers should pass a
   * summary like "XLM/USD daily candles"; falls back to a generic
   * label with the bar count.
   */
  ariaLabel?: string;
};

/**
 * CandleChart — TradingView Lightweight Charts wrapper: OHLC
 * candlesticks with an optional volume histogram underneath (rendered
 * when any bar carries a `volume`). The volume series sits on its own
 * overlaid price scale pinned to the bottom ~22% so it never collides
 * with the price axis.
 *
 * Per docs/architecture/explorer-implementation-plan.md §1.1 we use
 * Lightweight Charts (BSD, ~30 KB gzipped) over the full Charting
 * Library — drawing tools aren't needed at v1 and the bundle savings
 * matter for the per-route budget.
 *
 * The component owns the chart lifecycle: create on mount, apply
 * theme, dispose on unmount. Data updates push via setData rather than
 * tearing down the chart on re-render.
 */
export function CandleChart({ data, height = 360, className, ariaLabel }: CandleChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null);
  const volumeRef = useRef<ISeriesApi<'Histogram'> | null>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const chart = createChart(container, {
      layout: {
        background: { type: ColorType.Solid, color: 'transparent' },
        textColor: '#64748b', // slate-500
        fontFamily: 'var(--font-sans)',
        fontSize: 11,
      },
      grid: {
        horzLines: { color: 'rgba(148, 163, 184, 0.15)' }, // slate-400 @ 15%
        vertLines: { color: 'rgba(148, 163, 184, 0.10)' },
      },
      timeScale: {
        timeVisible: true,
        secondsVisible: false,
        borderColor: 'rgba(148, 163, 184, 0.25)',
        // Local-time axis labels — see ./localTime. The default is UTC,
        // which reads as "stale" against a viewer's wall clock.
        tickMarkFormatter: localTickMarkFormatter,
      },
      localization: {
        timeFormatter: localCrosshairTimeFormatter,
      },
      rightPriceScale: {
        borderColor: 'rgba(148, 163, 184, 0.25)',
        // Leave room at the bottom for the volume histogram.
        scaleMargins: { top: 0.08, bottom: 0.26 },
      },
      crosshair: {
        mode: 1, // CrosshairMode.Normal
      },
      width: container.clientWidth,
      height,
    });
    chartRef.current = chart;

    // lightweight-charts v5 series API: addSeries(SeriesDefinition, options).
    const series = chart.addSeries(CandlestickSeries, {
      upColor: '#16a34a', // up
      downColor: '#dc2626', // down
      wickUpColor: '#16a34a',
      wickDownColor: '#dc2626',
      borderVisible: false,
    });
    seriesRef.current = series;

    // Volume histogram on its own overlay scale, pinned to the bottom.
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
    // `data` is intentionally NOT a dep — the data effect below updates
    // without tearing down the chart. Effect ordering guarantees the
    // data effect fires after this one on first render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [height]);

  // Push new data on prop changes (and initial mount) without
  // destroying the chart.
  useEffect(() => {
    seriesRef.current?.setData(toSeries(data));
    volumeRef.current?.setData(toVolume(data));
    chartRef.current?.timeScale().fitContent();
  }, [data]);

  return (
    <div
      ref={containerRef}
      className={className}
      style={{ width: '100%', height }}
      role="img"
      aria-label={
        ariaLabel ??
        `Candlestick price chart${data.length ? ` with ${data.length} bars` : ''}`
      }
    />
  );
}

function toSeries(points: CandlePoint[]): CandlestickData<Time>[] {
  return points.map((p) => ({
    time: p.time as Time,
    open: p.open,
    high: p.high,
    low: p.low,
    close: p.close,
  }));
}

// Volume bars, tinted to the bar's direction (green when close ≥ open,
// red otherwise) at low opacity so they read as context, not foreground.
function toVolume(points: CandlePoint[]): HistogramData<Time>[] {
  return points
    .filter((p) => p.volume != null && Number.isFinite(p.volume))
    .map((p) => ({
      time: p.time as Time,
      value: p.volume as number,
      color: p.close >= p.open ? 'rgba(22, 163, 74, 0.45)' : 'rgba(220, 38, 38, 0.45)',
    }));
}
