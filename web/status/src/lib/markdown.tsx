// Minimal markdown block renderer for incident postmortems.
// Mirrors the explorer's lib/markdown.tsx so authored content
// renders the same on both sites — h1–h4, paragraphs, lists,
// fenced code, blockquotes, plus inline bold/code/link.

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
  // Strip HTML comments — incident posts use them for in-template
  // editor notes that should not render.
  md = md.replace(/<!--[\s\S]*?-->/g, '');
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
    <div className="space-y-4">{blocks.map((b, i) => renderBlock(b, i))}</div>
  );
}

function renderBlock(b: Block, i: number): React.ReactElement {
  switch (b.kind) {
    case 'h1':
      return (
        <h1 key={i} className="mt-8 text-2xl font-semibold tracking-tight text-ink">
          <Inline text={b.text} />
        </h1>
      );
    case 'h2':
      return (
        <h2
          key={i}
          className="mt-8 text-xl font-semibold tracking-tight text-ink border-b border-surface-line pb-1"
        >
          <Inline text={b.text} />
        </h2>
      );
    case 'h3':
      return (
        <h3 key={i} className="mt-6 text-base font-semibold text-ink">
          <Inline text={b.text} />
        </h3>
      );
    case 'h4':
      return (
        <h4 key={i} className="mt-4 text-sm font-semibold uppercase tracking-wider text-ink-faint">
          <Inline text={b.text} />
        </h4>
      );
    case 'p':
      return (
        <p key={i} className="text-sm leading-6 text-ink-muted">
          <Inline text={b.text} />
        </p>
      );
    case 'ul':
      return (
        <ul key={i} className="ml-5 list-disc space-y-1 text-sm leading-6 text-ink-muted">
          {b.items.map((it, j) => (
            <li key={j}>
              <Inline text={it} />
            </li>
          ))}
        </ul>
      );
    case 'ol':
      return (
        <ol key={i} className="ml-5 list-decimal space-y-1 text-sm leading-6 text-ink-muted">
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
          className="overflow-x-auto rounded-md border border-surface-line bg-surface-subtle p-3 text-xs leading-5"
        >
          <code>{b.code}</code>
        </pre>
      );
    case 'blockquote':
      return (
        <blockquote
          key={i}
          className="border-l-2 border-surface-line pl-4 text-sm italic text-ink-muted"
        >
          <Inline text={b.text} />
        </blockquote>
      );
    case 'hr':
      return <hr key={i} className="border-surface-line" />;
  }
}

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
              className="rounded bg-surface-subtle px-1 py-0.5 font-mono text-[0.85em]"
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
              className="text-brand-600 hover:underline"
            >
              {t.value}
            </a>
          );
        return <span key={i}>{t.value}</span>;
      })}
    </>
  );
}
