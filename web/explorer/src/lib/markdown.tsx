// Minimal markdown block renderer for embedded docs (ADRs,
// incident postmortems). Handles the shapes our authored docs
// actually use — h1-h4, paragraphs, ordered + unordered lists
// (single-level), fenced code blocks, blockquotes, GFM pipe tables
// — plus the same inline tokenizer the changelog page uses
// (bold/code/link).
//
// Deliberately minimal. If a doc grows nested lists or other
// constructs, we'll graduate to remark — but pulling a 30 kB parser
// into the static bundle for this handful of shapes is overkill today.

import React from 'react';

type Block =
  | { kind: 'h1'; text: string }
  | { kind: 'h2'; text: string }
  | { kind: 'h3'; text: string }
  | { kind: 'h4'; text: string }
  | { kind: 'p'; text: string }
  | { kind: 'ul'; items: string[] }
  | { kind: 'ol'; items: string[] }
  | { kind: 'pre'; lang: string; code: string }
  | { kind: 'blockquote'; text: string }
  | { kind: 'table'; headers: string[]; rows: string[][] }
  | { kind: 'hr' };

function tokenize(md: string): Block[] {
  const lines = md.split('\n');
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    if (line.startsWith('```')) {
      const lang = line.slice(3).trim();
      const buf: string[] = [];
      i++;
      while (i < lines.length && !lines[i]!.startsWith('```')) {
        buf.push(lines[i]!);
        i++;
      }
      i++;
      out.push({ kind: 'pre', lang, code: buf.join('\n') });
      continue;
    }
    if (line.startsWith('#### ')) {
      out.push({ kind: 'h4', text: line.slice(5) });
      i++;
      continue;
    }
    if (line.startsWith('### ')) {
      out.push({ kind: 'h3', text: line.slice(4) });
      i++;
      continue;
    }
    if (line.startsWith('## ')) {
      out.push({ kind: 'h2', text: line.slice(3) });
      i++;
      continue;
    }
    if (line.startsWith('# ')) {
      out.push({ kind: 'h1', text: line.slice(2) });
      i++;
      continue;
    }
    if (/^---+\s*$/.test(line)) {
      out.push({ kind: 'hr' });
      i++;
      continue;
    }
    if (line.startsWith('> ')) {
      const buf: string[] = [];
      while (i < lines.length && lines[i]!.startsWith('> ')) {
        buf.push(lines[i]!.slice(2));
        i++;
      }
      out.push({ kind: 'blockquote', text: buf.join(' ') });
      continue;
    }
    const ulMatch = line.match(/^[-*]\s+(.*)$/);
    if (ulMatch) {
      const items: string[] = [ulMatch[1]!];
      i++;
      while (i < lines.length) {
        const m = lines[i]!.match(/^[-*]\s+(.*)$/);
        if (!m) break;
        items.push(m[1]!);
        i++;
      }
      out.push({ kind: 'ul', items });
      continue;
    }
    const olMatch = line.match(/^\d+\.\s+(.*)$/);
    if (olMatch) {
      const items: string[] = [olMatch[1]!];
      i++;
      while (i < lines.length) {
        const m = lines[i]!.match(/^\d+\.\s+(.*)$/);
        if (!m) break;
        items.push(m[1]!);
        i++;
      }
      out.push({ kind: 'ol', items });
      continue;
    }
    // GFM table: a header row with '|', then a delimiter row (| --- | :--: |),
    // then body rows. Without this, table lines fall through to the paragraph
    // branch and render as a wall of literal pipes.
    const isDelimRow = (l: string) =>
      /^[\s|:-]+$/.test(l) && l.includes('-') && l.includes('|');
    if (line.includes('|') && i + 1 < lines.length && isDelimRow(lines[i + 1]!)) {
      const splitRow = (r: string): string[] => {
        let cells = r.trim().split('|');
        if (cells[0] === '') cells = cells.slice(1);
        if (cells.length && cells[cells.length - 1] === '') cells = cells.slice(0, -1);
        return cells.map((c) => c.trim());
      };
      const headers = splitRow(line);
      i += 2; // consume header + delimiter
      const rows: string[][] = [];
      while (i < lines.length && lines[i]!.includes('|') && lines[i]!.trim() !== '') {
        rows.push(splitRow(lines[i]!));
        i++;
      }
      out.push({ kind: 'table', headers, rows });
      continue;
    }
    if (line.trim() === '') {
      i++;
      continue;
    }
    // Paragraph — gather contiguous non-empty non-special lines.
    const buf: string[] = [line];
    i++;
    while (
      i < lines.length &&
      lines[i]!.trim() !== '' &&
      !lines[i]!.startsWith('#') &&
      !lines[i]!.startsWith('```') &&
      !lines[i]!.startsWith('> ') &&
      !lines[i]!.match(/^[-*]\s+/) &&
      !lines[i]!.match(/^\d+\.\s+/)
    ) {
      buf.push(lines[i]!);
      i++;
    }
    out.push({ kind: 'p', text: buf.join(' ') });
  }
  return out;
}

const GH_BLOB = 'https://github.com/StellarIndex/stellar-index/blob/main/';
const GH_TREE = 'https://github.com/StellarIndex/stellar-index/tree/main/';

// resolveDocLink turns a repo-relative markdown link (authored in the doc at
// `sourcePath`, relative to ITS directory) into a URL that actually resolves
// on the web. Without this, links like `../adr/0015-x.md` or
// `../../internal/x.go` render as literal hrefs and 404 on /research/* pages.
// ADR cross-refs map to their in-site page (every docs/adr/NNNN-*.md renders);
// everything else (sibling docs, code, CHANGELOG, configs) maps to the GitHub
// source, which always resolves.
export function resolveDocLink(href: string, sourcePath: string): string {
  const h = href.trim();
  if (/^(https?:|mailto:|tel:|#|\/)/i.test(h)) return href; // external / anchor / absolute
  const m = h.match(/^([^#?]*)([#?].*)?$/);
  const rel = m ? m[1]! : h;
  const suffix = m && m[2] ? m[2] : '';
  const stack = sourcePath.split('/').slice(0, -1);
  for (const seg of rel.split('/')) {
    if (seg === '..') stack.pop();
    else if (seg !== '.' && seg !== '') stack.push(seg);
  }
  const target = stack.join('/');
  if (!target) return href;
  const adr = target.match(/^docs\/adr\/(\d{4})-[^/]+\.md$/);
  if (adr) return `/research/adr/${adr[1]}/${suffix}`; // trailing slash = site convention (no 308 hop)
  const last = target.split('/').pop() ?? '';
  const isDir = rel.endsWith('/') || !last.includes('.');
  return `${isDir ? GH_TREE : GH_BLOB}${target}${suffix}`;
}

function rewriteDocLinks(md: string, sourcePath: string): string {
  return md.replace(
    /\]\(([^)\s]+)(\s+"[^"]*")?\)/g,
    (_full, href: string, title?: string) =>
      `](${resolveDocLink(href, sourcePath)}${title ?? ''})`,
  );
}

export function Markdown({
  source,
  sourcePath,
}: {
  source: string;
  // Repo-relative path of the doc being rendered (e.g.
  // "docs/architecture/aggregation-plan.md"). When set, repo-relative links
  // in the body are rewritten to working web/GitHub URLs.
  sourcePath?: string;
}) {
  const md = sourcePath ? rewriteDocLinks(source, sourcePath) : source;
  const blocks = tokenize(md);
  return (
    <div className="prose-readable space-y-4">
      {blocks.map((b, i) => renderBlock(b, i))}
    </div>
  );
}

function renderBlock(b: Block, i: number): React.ReactElement {
  switch (b.kind) {
    case 'h1':
      return (
        <h1 key={i} className="mt-8 text-2xl font-semibold tracking-tight">
          <Inline text={b.text} />
        </h1>
      );
    case 'h2':
      return (
        <h2
          key={i}
          className="mt-8 text-xl font-semibold tracking-tight border-b border-line pb-1"
        >
          <Inline text={b.text} />
        </h2>
      );
    case 'h3':
      return (
        <h3 key={i} className="mt-6 text-base font-semibold">
          <Inline text={b.text} />
        </h3>
      );
    case 'h4':
      return (
        <h4 key={i} className="mt-4 text-sm font-semibold uppercase tracking-wider text-ink-muted">
          <Inline text={b.text} />
        </h4>
      );
    case 'p':
      return (
        <p key={i} className="text-sm leading-6 text-ink-body">
          <Inline text={b.text} />
        </p>
      );
    case 'ul':
      return (
        <ul key={i} className="ml-5 list-disc space-y-1 text-sm leading-6 text-ink-body">
          {b.items.map((it, j) => (
            <li key={j}>
              <Inline text={it} />
            </li>
          ))}
        </ul>
      );
    case 'ol':
      return (
        <ol key={i} className="ml-5 list-decimal space-y-1 text-sm leading-6 text-ink-body">
          {b.items.map((it, j) => (
            <li key={j}>
              <Inline text={it} />
            </li>
          ))}
        </ol>
      );
    case 'pre':
      return (
        <pre
          key={i}
          className="overflow-x-auto rounded-lg border border-line bg-surface-muted p-3 text-xs leading-5"
        >
          <code>{b.code}</code>
        </pre>
      );
    case 'blockquote':
      return (
        <blockquote
          key={i}
          className="border-l-2 border-line-strong pl-4 text-sm italic text-ink-body"
        >
          <Inline text={b.text} />
        </blockquote>
      );
    case 'table':
      return (
        <div key={i} className="overflow-x-auto">
          <table className="w-full border-collapse text-sm leading-6 text-ink-body">
            <thead>
              <tr className="border-b border-line-strong text-left">
                {b.headers.map((h, j) => (
                  <th key={j} className="px-3 py-2 font-semibold text-ink">
                    <Inline text={h} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {b.rows.map((row, r) => (
                <tr key={r} className="border-b border-line align-top">
                  {row.map((cell, c) => (
                    <td key={c} className="px-3 py-2">
                      <Inline text={cell} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    case 'hr':
      return <hr key={i} className="border-line" />;
  }
}

// isSafeHref allowlists the URL schemes a markdown link may carry.
// The corpus is build-time-trusted today, but defence-in-depth: a
// `[click](javascript:alert(1))` link must never render as a live
// anchor. We permit absolute http/https/mailto and relative refs
// (path / anchor / query). Anything else (javascript:, data:,
// vbscript:, file:, …) is rejected and rendered as plain text.
function isSafeHref(href: string): boolean {
  const h = href.trim();
  if (h === '') return false;
  // Relative references — no scheme, can't be javascript:.
  if (/^[/#?]/.test(h) || h.startsWith('./') || h.startsWith('../')) {
    return true;
  }
  // A scheme is everything up to the first ':' (before any /?#).
  const schemeMatch = h.match(/^([a-zA-Z][a-zA-Z0-9+.-]*):/);
  if (!schemeMatch) {
    // No scheme and not caught above (e.g. "example.com/x" or a bare
    // word) — treat as relative, which is harmless.
    return true;
  }
  const scheme = schemeMatch[1]!.toLowerCase();
  return scheme === 'http' || scheme === 'https' || scheme === 'mailto';
}

// Inline tokenizer — same shape as MarkdownLite in changelog/page.tsx
// but kept here so research pages don't depend on changelog code.
function Inline({ text }: { text: string }) {
  type Tok =
    | { kind: 'text'; value: string }
    | { kind: 'bold'; value: string }
    | { kind: 'code'; value: string }
    | { kind: 'link'; value: string; href: string };
  const tokens: Tok[] = [];
  let rest = text;
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
            <strong key={i} className="font-semibold text-ink">
              {t.value}
            </strong>
          );
        if (t.kind === 'code')
          return (
            <code
              key={i}
              className="rounded-sm bg-surface-subtle px-1 py-0.5 font-mono text-[0.85em]"
            >
              {t.value}
            </code>
          );
        if (t.kind === 'link') {
          // Reject disallowed schemes (javascript:, data:, …) — render
          // the label as plain text rather than a live anchor.
          if (!isSafeHref(t.href)) {
            return <span key={i}>{t.value}</span>;
          }
          return (
            <a
              key={i}
              href={t.href}
              target={t.href.startsWith('http') ? '_blank' : undefined}
              rel={t.href.startsWith('http') ? 'noreferrer noopener' : undefined}
              className="text-brand-600 hover:underline"
            >
              {t.value}
            </a>
          );
        }
        return <span key={i}>{t.value}</span>;
      })}
    </>
  );
}
