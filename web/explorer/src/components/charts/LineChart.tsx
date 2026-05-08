'use client';

import { useEffect, useRef } from 'react';
import {
  ColorType,
  createChart,
  type IChartApi,
  type ISeriesApi,
  type LineData,
  type Time,
} from 'lightweight-charts';

export type LinePoint = {
  /** Unix epoch seconds */
  time: number;
  value: number;
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
};

/**
 * LineChart — TradingView Lightweight Charts wrapper for daily
 * scalar series (typically fx rates or other 1d-grain data).
 *
 * Companion to [CandleChart] — same lifecycle, theme, and resize
 * behaviour. Use this when you have (time, value) points; use
 * CandleChart when you have OHLC.
 */
export function LineChart({
  data,
  height = 320,
  className,
  positive,
}: LineChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<'Area'> | null>(null);

  // Resolve trend tone — first vs last value when the caller doesn't
  // specify. Renders green when the series is up, red when down,
  // neutral grey-blue when flat or empty.
  const isUp = positive ?? trendUp(data);

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
        timeVisible: false,
        secondsVisible: false,
        borderColor: 'rgba(148, 163, 184, 0.25)',
      },
      rightPriceScale: {
        borderColor: 'rgba(148, 163, 184, 0.25)',
      },
      crosshair: {
        mode: 1,
      },
      width: container.clientWidth,
      height,
    });
    chartRef.current = chart;

    const lineColor = isUp ? '#059669' : '#e11d48';
    const fillColor = isUp ? 'rgba(16, 185, 129, 0.15)' : 'rgba(244, 63, 94, 0.15)';
    const series = chart.addAreaSeries({
      lineColor,
      topColor: fillColor,
      bottomColor: 'rgba(0,0,0,0)',
      lineWidth: 2,
      priceLineVisible: false,
    });
    seriesRef.current = series;

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
    // height + isUp drive chart re-creation; data updates are
    // pushed via setData in the second effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [height, isUp]);

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

function toSeries(points: LinePoint[]): LineData<Time>[] {
  return points.map((p) => ({
    time: p.time as Time,
    value: p.value,
  }));
}

function trendUp(points: LinePoint[]): boolean {
  if (points.length < 2) return true;
  return points[points.length - 1].value >= points[0].value;
}
