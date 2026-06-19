import { type Time, TickMarkType } from 'lightweight-charts';

// lightweight-charts plots `time` as UTC and, by default, labels both
// the axis ticks and the crosshair in UTC. On an intraday chart that
// means the current hour reads an hour (or more) "behind" the viewer's
// wall clock — e.g. a bar at 15:00 UTC shown to a UTC+1 viewer whose
// clock says 16:xx, which looks like missing/stale data.
//
// These formatters relabel in the viewer's LOCAL timezone WITHOUT
// shifting bar positions (so no data distortion, correct in every
// timezone). The split is deliberate:
//   - time-of-day labels → LOCAL (this is what makes the axis match the
//     viewer's clock — the whole point).
//   - date labels (day / month / year) → UTC, because a daily or weekly
//     candle IS a UTC calendar bucket; localising its date would push a
//     midnight-UTC bar onto the previous local day for western viewers.
// The tiny skew this creates at a day boundary (a UTC-date tick sitting
// near a local-time tick) is immaterial next to "the current bar shows
// my local time".

const HHMM: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' };

export function localTickMarkFormatter(time: Time, tickMarkType: TickMarkType): string {
  const d = new Date((time as number) * 1000);
  switch (tickMarkType) {
    case TickMarkType.Year:
      return String(d.getUTCFullYear());
    case TickMarkType.Month:
      return d.toLocaleDateString(undefined, { month: 'short', year: '2-digit', timeZone: 'UTC' });
    case TickMarkType.DayOfMonth:
      return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', timeZone: 'UTC' });
    default: // Time / TimeWithSeconds — the viewer's local clock
      return d.toLocaleTimeString(undefined, HHMM);
  }
}

// Crosshair label (the floating time on the axis when hovering). Local
// wall-clock — date + time — so the readout matches the viewer's clock.
export function localCrosshairTimeFormatter(time: Time): string {
  const d = new Date((time as number) * 1000);
  return d.toLocaleString(undefined, { month: 'short', day: 'numeric', ...HHMM });
}
