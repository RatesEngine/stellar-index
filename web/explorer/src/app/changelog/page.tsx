import type { Metadata } from 'next';
import Link from 'next/link';

import { loadReleases, type Release } from '@/lib/changelog';

export const metadata: Metadata = {
  title: 'Changelog',
  description:
    'Every release of Rates Engine — features added, bugs fixed, and the architectural changes behind them. Source: CHANGELOG.md.',
};

export default function ChangelogPage() {
  const releases: Release[] = loadReleases();
  return (
    <div className="mx-auto max-w-4xl space-y-8 px-6 py-10">
      <header className="space-y-3">
        <div className="flex items-baseline justify-between">
          <p className="font-mono text-xs uppercase tracking-widest text-brand-600 dark:text-brand-400">
            Changelog
          </p>
          <a
            href="/changelog.atom"
            target="_blank"
            rel="noreferrer noopener"
            className="text-xs text-slate-500 hover:text-brand-600"
            title="Atom feed — subscribe in Feedly, Slack RSS bot, etc."
          >
            Subscribe (Atom) ↗
          </a>
        </div>
        <h1 className="text-4xl font-semibold tracking-tight">
          Every release, every change.
        </h1>
        <p className="max-w-2xl text-base text-slate-600 dark:text-slate-400">
          Pulled at build time from{' '}
          <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-sm dark:bg-slate-800">
            CHANGELOG.md
          </code>{' '}
          on{' '}
          <a
            href="https://github.com/RatesEngine/rates-engine/blob/main/CHANGELOG.md"
            target="_blank"
            rel="noreferrer noopener"
            className="text-brand-600 hover:underline dark:text-brand-400"
          >
            main
          </a>
          . Format follows{' '}
          <a
            href="https://keepachangelog.com/en/1.1.0/"
            target="_blank"
            rel="noreferrer noopener"
            className="text-brand-600 hover:underline dark:text-brand-400"
          >
            Keep a Changelog
          </a>
          ; SemVer for the public Go SDK, CalVer for binary releases.
        </p>
      </header>

      {releases.length === 0 ? (
        <div className="rounded-md border border-amber-200 bg-amber-50 p-6 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200">
          CHANGELOG.md not found at build time — this page is a
          stub. See the{' '}
          <a
            href="https://github.com/RatesEngine/rates-engine/blob/main/CHANGELOG.md"
            target="_blank"
            rel="noreferrer noopener"
            className="underline"
          >
            canonical changelog on GitHub
          </a>
          .
        </div>
      ) : (
        <div className="space-y-10">
          {releases.map((r) => (
            <ReleaseCard key={r.version} release={r} />
          ))}
        </div>
      )}

      <div className="border-t border-slate-200 pt-6 text-sm text-slate-500 dark:border-slate-800">
        <Link href="/" className="text-brand-600 hover:underline">
          ← Home
        </Link>
        <span className="mx-2">·</span>
        <a
          href="https://github.com/RatesEngine/rates-engine/releases"
          target="_blank"
          rel="noreferrer noopener"
          className="text-brand-600 hover:underline"
        >
          GitHub Releases ↗
        </a>
      </div>
    </div>
  );
}

function ReleaseCard({ release }: { release: Release }) {
  const isUnreleased = release.version.toLowerCase() === 'unreleased';
  return (
    <article className="rounded-lg border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <header className="mb-4 flex flex-wrap items-baseline justify-between gap-2 border-b border-slate-100 pb-3 dark:border-slate-800">
        <h2 className="font-mono text-2xl font-semibold tracking-tight">
          {release.version}
        </h2>
        <div className="flex items-center gap-2 text-xs">
          {isUnreleased ? (
            <span className="rounded bg-amber-100 px-2 py-0.5 font-mono uppercase tracking-wider text-amber-800 dark:bg-amber-900/40 dark:text-amber-200">
              unreleased
            </span>
          ) : (
            release.date && (
              <span className="font-mono tabular-nums text-slate-500">
                {release.date}
              </span>
            )
          )}
          {!isUnreleased && (
            <a
              href={`https://github.com/RatesEngine/rates-engine/releases/tag/${release.version}`}
              target="_blank"
              rel="noreferrer noopener"
              className="rounded border border-slate-200 px-2 py-0.5 font-mono text-xs hover:border-brand-500 hover:text-brand-600 dark:border-slate-700"
            >
              GitHub ↗
            </a>
          )}
        </div>
      </header>
      <div className="space-y-4">
        {release.blocks.map((b, i) => (
          <BlockSection key={i} block={b} />
        ))}
      </div>
    </article>
  );
}

function BlockSection({ block }: { block: { kind: string; lines: string[] } }) {
  const tone =
    block.kind === 'Added'
      ? 'text-emerald-700 dark:text-emerald-400'
      : block.kind === 'Fixed'
        ? 'text-sky-700 dark:text-sky-400'
        : block.kind === 'Changed'
          ? 'text-amber-700 dark:text-amber-400'
          : block.kind === 'Removed' || block.kind === 'Deprecated'
            ? 'text-rose-700 dark:text-rose-400'
            : 'text-slate-700 dark:text-slate-300';

  // Strip the leading "- " bullet from list items so we can group
  // by sub-item; preserve everything else as raw markdown the
  // browser doesn't have to parse heavily — bullet lines start a
  // new entry, indented continuation lines glue to the previous.
  const items: string[] = [];
  let buf = '';
  for (const line of block.lines) {
    if (/^- /.test(line)) {
      if (buf) items.push(buf);
      buf = line.replace(/^- /, '');
    } else if (buf) {
      buf += '\n' + line;
    }
  }
  if (buf) items.push(buf);

  return (
    <section>
      <h3 className={`mb-2 text-xs font-semibold uppercase tracking-wider ${tone}`}>
        {block.kind}
      </h3>
      <ul className="space-y-2 text-sm text-slate-700 dark:text-slate-300">
        {items.map((it, i) => (
          <li key={i}>
            <MarkdownLite text={it} />
          </li>
        ))}
      </ul>
    </section>
  );
}

/**
 * MarkdownLite — bare-bones renderer for the subset of markdown we
 * use in CHANGELOG entries: `**bold**`, `code`, and `[label](url)`
 * links. Anything more (lists nested in lists, tables, images)
 * passes through as plain text. Avoids pulling in a full markdown
 * parser for ~5 inline shapes.
 */
function MarkdownLite({ text }: { text: string }) {
  // Tokenize the line into a flat array of {kind, value} so React
  // can render each fragment with appropriate styling.
  type Tok =
    | { kind: 'text'; value: string }
    | { kind: 'bold'; value: string }
    | { kind: 'code'; value: string }
    | { kind: 'link'; value: string; href: string };
  const tokens: Tok[] = [];
  let rest = text;
  // Order matters: link first (contains brackets that bold could
  // accidentally consume), then code, then bold.
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
      tokens.push({ kind: 'text', value: rest[0]! });
      rest = rest.slice(1);
      // Coalesce sequential text tokens.
      if (
        tokens.length >= 2 &&
        tokens[tokens.length - 1]!.kind === 'text' &&
        tokens[tokens.length - 2]!.kind === 'text'
      ) {
        const a = tokens.pop()! as Tok & { kind: 'text' };
        const b = tokens.pop()! as Tok & { kind: 'text' };
        tokens.push({ kind: 'text', value: b.value + a.value });
      }
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
            <a
              key={i}
              href={t.href}
              target={t.href.startsWith('http') ? '_blank' : undefined}
              rel={t.href.startsWith('http') ? 'noreferrer noopener' : undefined}
              className="text-brand-600 hover:underline dark:text-brand-400"
            >
              {t.value}
            </a>
          );
        return <span key={i}>{t.value}</span>;
      })}
    </>
  );
}
