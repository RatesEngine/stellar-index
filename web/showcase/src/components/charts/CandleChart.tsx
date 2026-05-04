'use client';

import { useEffect, useRef } from 'react';
import {
  ColorType,
  createChart,
  type CandlestickData,
  type IChartApi,
  type ISeriesApi,
  type Time,
} from 'lightweight-charts';

export type CandlePoint = {
  /** Unix epoch seconds */
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
};

export type CandleChartProps = {
  data: CandlePoint[];
  height?: number;
  className?: string;
};

/**
 * CandleChart — TradingView Lightweight Charts wrapper.
 *
 * Per docs/architecture/showcase-site-implementation-plan.md §1.1
 * we use Lightweight Charts (BSD, ~30 KB gzipped) over the full
 * Charting Library — drawing tools aren't needed at v1 and the
 * bundle savings matter for the per-route 100 KB budget.
 *
 * The component owns the chart lifecycle: create on mount, fit
 * content + apply theme, dispose on unmount. Updates push new data
 * via setData rather than tearing down the chart on every re-render.
 *
 * Theme: defaults match the slate-100/950 backgrounds used by
 * Panel chrome. Override via CSS custom properties if a panel
 * needs a different palette.
 */
export function CandleChart({ data, height = 360, className }: CandleChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null);

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
      },
      rightPriceScale: {
        borderColor: 'rgba(148, 163, 184, 0.25)',
      },
      crosshair: {
        mode: 1, // CrosshairMode.Normal
      },
      width: container.clientWidth,
      height,
    });
    chartRef.current = chart;

    // Prefer the v4 API. addCandlestickSeries is named differently in
    // v5 (`addSeries(CandlestickSeries)`) — pin v4 on package.json so
    // the call below stays valid.
    const series = chart.addCandlestickSeries({
      upColor: '#16a34a', // up-DEFAULT
      downColor: '#dc2626', // down-DEFAULT
      wickUpColor: '#16a34a',
      wickDownColor: '#dc2626',
      borderVisible: false,
    });
    seriesRef.current = series;

    series.setData(toSeries(data));
    chart.timeScale().fitContent();

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
    };
  }, [height]);

  // Push new data on prop changes without destroying the chart.
  useEffect(() => {
    seriesRef.current?.setData(toSeries(data));
    chartRef.current?.timeScale().fitContent();
  }, [data]);

  return (
    <div
      ref={containerRef}
      className={className}
      style={{ width: '100%', height }}
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
