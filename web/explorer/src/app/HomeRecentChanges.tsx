import Link from 'next/link';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

interface ChangeItem {
  release: string;
  date?: string;
  kind: string;
  text: string;
}

/**
 * HomeRecentChanges — top-of-fold "Recently shipped" widget.
 *
 * Renders the 3 most recent changelog bullets from the latest
 * non-Unreleased release block in CHANGELOG.md. Helps a returning
 * visitor immediately see "what changed since I was last here?"
 * without clicking through to /changelog.
 *
 * Server-component; reads CHANGELOG.md at build time. The full
 * /changelog page does the same parse for everything; this just
 * keeps the head of the head.
 */
export function HomeRecentChanges() {
  const items = readRecentItems(3);
  if (items.length === 0) return null;
  return (
    <section className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div className="space-y-1">
          <h2 className="text-2xl font-semibold tracking-tight">
            Recently shipped
          </h2>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            What landed in the last release. Scrolling history at{' '}
            <Link href="/changelog" className="text-brand-600 hover:underline">
              /changelog
            </Link>
            .
          </p>
        </div>
        <div className="flex items-center gap-3 text-xs">
          <a
            href="/changelog.atom"
            target="_blank"
            rel="noreferrer noopener"
            className="text-slate-500 hover:text-brand-600"
            title="Atom feed — subscribe in Feedly, Slack RSS bot, etc."
          >
            Subscribe (Atom) ↗
          </a>
          <Link href="/changelog" className="text-brand-600 hover:underline">
            Full changelog →
          </Link>
        </div>
      </div>
      <ul className="space-y-2">
        {items.map((it, i) => (
          <li
            key={i}
            className="rounded-md border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900"
          >
            <div className="mb-1 flex items-baseline gap-2">
              <span
                className={`rounded px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider ${kindTone(
                  it.kind,
                )}`}
              >
                {it.kind}
              </span>
              <span className="font-mono text-[10px] text-slate-400">
                {it.release}
                {it.date && ` · ${it.date}`}
              </span>
            </div>
            <p className="text-sm text-slate-700 dark:text-slate-300">
              <ChangelogPreview text={it.text} />
            </p>
          </li>
        ))}
      </ul>
    </section>
  );
}

// readRecentItems pulls the first N bullet entries from the most
// recent non-Unreleased release in CHANGELOG.md. Failures (file
// missing, no parseable releases) return [] so the home page
// silently omits the widget rather than blowing up the build.
function readRecentItems(n: number): ChangeItem[] {
  let text = '';
  try {
    text = readFileSync(join(process.cwd(), '../../CHANGELOG.md'), 'utf8');
  } catch {
    return [];
  }
  const lines = text.split('\n');
  let release = '';
  let date: string | undefined;
  let kind = '';
  let foundReleased = false;
  const out: ChangeItem[] = [];
  let buf = '';

  function flushBuf() {
    if (buf && release && kind) {
      out.push({ release, date, kind, text: buf.trim() });
      buf = '';
    }
  }

  for (const line of lines) {
    const releaseMatch = line.match(/^##\s+\[([^\]]+)\](?:\s+—\s+(\S+))?/);
    if (releaseMatch) {
      flushBuf();
      if (out.length >= n && foundReleased) break;
      release = releaseMatch[1]!;
      date = releaseMatch[2];
      kind = '';
      foundReleased = release.toLowerCase() !== 'unreleased';
      // We render Unreleased too — fresh main commits should
      // surface. The "last release" framing is fuzzy on purpose.
      continue;
    }
    if (!release) continue;
    const blockMatch = line.match(/^###\s+(.+)$/);
    if (blockMatch) {
      flushBuf();
      kind = blockMatch[1]!.trim();
      continue;
    }
    if (out.length >= n) continue;
    if (/^- /.test(line)) {
      flushBuf();
      buf = line.replace(/^- /, '');
    } else if (buf) {
      buf += '\n' + line;
    }
  }
  flushBuf();
  return out.slice(0, n);
}

function kindTone(kind: string): string {
  if (kind === 'Added')
    return 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200';
  if (kind === 'Fixed')
    return 'bg-sky-100 text-sky-800 dark:bg-sky-900/40 dark:text-sky-200';
  if (kind === 'Changed')
    return 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200';
  if (kind === 'Removed' || kind === 'Deprecated')
    return 'bg-rose-100 text-rose-800 dark:bg-rose-900/40 dark:text-rose-200';
  return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200';
}

// ChangelogPreview renders just the FIRST paragraph of a CHANGELOG
// entry — bold + code + link substitutions, then truncate at the
// first newline-newline (matches the visual rhythm where the bold
// heading is the "what" and the rest is the "why").
function ChangelogPreview({ text }: { text: string }) {
  const firstPara = text.split(/\n\n/)[0]!.trim();
  type Tok =
    | { kind: 'text'; value: string }
    | { kind: 'bold'; value: string }
    | { kind: 'code'; value: string }
    | { kind: 'link'; value: string; href: string };
  const tokens: Tok[] = [];
  let rest = firstPara;
  const patterns: { re: RegExp; mk: (m: RegExpMatchArray) => Tok }[] = [
    {
      re: /^\[([^\]]+)\]\(([^)]+)\)/,
      mk: (m) => ({ kind: 'link', value: m[1]!, href: m[2]! }),
    },
    { re: /^`([^`]+)`/, mk: (m) => ({ kind: 'code', value: m[1]! }) },
    { re: /^\*\*([^*]+)\*\*/, mk: (m) => ({ kind: 'bold', value: m[1]! }) },
  ];
  while (rest.length > 0) {
    let matched = false;
    for (const p of patterns) {
      const m = rest.match(p.re);
      if (m) {
        tokens.push(p.mk(m));
        rest = rest.slice(m[0].length);
        matched = true;
        break;
      }
    }
    if (!matched) {
      const last = tokens[tokens.length - 1];
      if (last && last.kind === 'text') {
        last.value += rest[0]!;
      } else {
        tokens.push({ kind: 'text', value: rest[0]! });
      }
      rest = rest.slice(1);
    }
  }
  return (
    <>
      {tokens.map((t, i) => {
        if (t.kind === 'bold')
          return (
            <strong key={i} className="font-semibold text-slate-900 dark:text-slate-100">
              {t.value}
            </strong>
          );
        if (t.kind === 'code')
          return (
            <code
              key={i}
              className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs dark:bg-slate-800"
            >
              {t.value}
            </code>
          );
        if (t.kind === 'link')
          return (
            <span key={i} className="text-brand-600">
              {t.value}
            </span>
          );
        return <span key={i}>{t.value}</span>;
      })}
    </>
  );
}
