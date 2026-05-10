// Minimal markdown block renderer for embedded docs (ADRs,
// incident postmortems). Handles the shapes our authored docs
// actually use — h1/h2/h3, paragraphs, ordered + unordered lists
// (single-level), fenced code blocks, blockquotes — plus the same
// inline tokenizer the changelog page uses (bold/code/link).
//
// Deliberately minimal. If a doc grows tables or nested lists,
// we'll graduate to remark — but pulling a 30 kB parser into the
// static bundle for ~5 inline shapes is overkill today.

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

export function Markdown({ source }: { source: string }) {
  const blocks = tokenize(source);
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
          className="mt-8 text-xl font-semibold tracking-tight border-b border-slate-200 pb-1 dark:border-slate-800"
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
        <h4 key={i} className="mt-4 text-sm font-semibold uppercase tracking-wider text-slate-500">
          <Inline text={b.text} />
        </h4>
      );
    case 'p':
      return (
        <p key={i} className="text-sm leading-6 text-slate-700 dark:text-slate-300">
          <Inline text={b.text} />
        </p>
      );
    case 'ul':
      return (
        <ul key={i} className="ml-5 list-disc space-y-1 text-sm leading-6 text-slate-700 dark:text-slate-300">
          {b.items.map((it, j) => (
            <li key={j}>
              <Inline text={it} />
            </li>
          ))}
        </ul>
      );
    case 'ol':
      return (
        <ol key={i} className="ml-5 list-decimal space-y-1 text-sm leading-6 text-slate-700 dark:text-slate-300">
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
          className="overflow-x-auto rounded-lg border border-slate-200 bg-slate-50 p-3 text-xs leading-5 dark:border-slate-800 dark:bg-slate-900"
        >
          <code>{b.code}</code>
        </pre>
      );
    case 'blockquote':
      return (
        <blockquote
          key={i}
          className="border-l-2 border-slate-300 pl-4 text-sm italic text-slate-600 dark:border-slate-600 dark:text-slate-400"
        >
          <Inline text={b.text} />
        </blockquote>
      );
    case 'hr':
      return <hr key={i} className="border-slate-200 dark:border-slate-800" />;
  }
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
            <strong key={i} className="font-semibold text-slate-900 dark:text-slate-100">
              {t.value}
            </strong>
          );
        if (t.kind === 'code')
          return (
            <code
              key={i}
              className="rounded bg-slate-100 px-1 py-0.5 font-mono text-[0.85em] dark:bg-slate-800"
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
