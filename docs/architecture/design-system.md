---
title: Design system — Stellar Index web (v2)
date: 2026-06-17
status: active
last_verified: 2026-06-17
---

# Design system

The shared visual language for every Stellar Index web surface — the public
explorer (`web/explorer`), the status page (`web/status`), and the customer
dashboard (`web/dashboard`). One system, applied everywhere.

**Live reference:** `/dev/styleguide` in the explorer renders every token and
component. **Tokens:** `web/explorer/tailwind.config.ts`. **Base layer:**
`web/explorer/src/app/globals.css`. **Components:** `web/explorer/src/components/ui/`.

## Principles

1. **Light mode only.** No dark mode for now. Design for a near-white canvas.
2. **Minimal, tech-forward, lots of whitespace.** Lean on hairline borders and
   generous spacing, not heavy shadows or chrome. Let data breathe.
3. **One accent.** A single confident blue (`brand`) carries actions, links,
   and selection. Everything else is neutral ink + surfaces. Semantic colour
   (up/down, warn/bad/ok) is reserved for meaning, never decoration.
4. **Inter + JetBrains Mono.** Inter for UI; JetBrains Mono for addresses,
   hashes, code, and numeric columns. Numbers that align in columns use
   tabular figures (`.tnum` / `font-mono`).
5. **Reuse the primitives.** Build pages from `@/components/ui`, not bespoke
   markup. If a pattern recurs, promote it to a primitive.

## Tokens

| Group | Tokens | Use |
|---|---|---|
| **brand** | `brand-50…950` (primary `brand-600`) | Actions, links, focus, active state, selection |
| **surface** | `surface` (white), `surface-canvas` (page bg), `surface-muted`, `surface-subtle` | Layering: canvas → card → recessed well |
| **line** | `line-subtle`, `line`, `line-strong` | Hairline borders (the default `border` colour is `line`) |
| **ink** | `ink` (headings), `ink-body`, `ink-muted`, `ink-faint` | Text hierarchy |
| **up / down** | `up`, `down` (+ `subtle`/`strong`) | Price deltas only |
| **warn / bad / ok** | `*-50/300/500/700` | Severity (banners, status, validation) |
| **type** | `text-display`, `display-sm`, `h1`, `h2`, `h3` | Headings (tight tracking baked in) |
| **radius** | `rounded-card` (14px), `rounded-lg`, `rounded-full` | Cards use `rounded-card` |
| **shadow** | `shadow-xs`, `shadow-card`, `shadow-elevated`, `shadow-focus` | Soft, low-opacity — used sparingly over hairlines |
| **width** | `max-w-page` (1280px), `max-w-prose` | Page frame / reading measure |

## Components (`@/components/ui`)

`Button`/`ButtonLink`, `Card`/`CardHeader`/`CardBody`/`CardFooter`, `Badge`,
`Stat`/`StatGrid`/`StatCell`, `Table` primitives, `Container`/`Section`/
`PageHeader`/`Breadcrumbs`/`SectionHeader`, `Input`/`Textarea`/`Select`/`Field`,
`EmptyState`/`Skeleton`/`Callout`, `TabNav`/`Segmented`, `Mono`/`CopyButton`.

Compose class strings with `cn()` (`@/lib/cn`) — clsx + tailwind-merge, so
overrides resolve last-wins without specificity fights.

## Patterns

- **Page scaffold:** `<Container><Section><PageHeader …/> … </Section></Container>`.
- **Metric strip:** `StatGrid` of `StatCell` + `Stat` (tabular figures).
- **Data table:** `TableWrap > Table > THead/TBody`; right-align + `tnum` numbers.
- **Empty / loading:** `EmptyState` for no-data, `Skeleton` for loading.
- **Identifiers:** `Mono` with `truncate` for G-strkeys / C-ids / tx hashes.

## Do / Don't

- ✅ Hairline borders + whitespace to separate. ❌ Heavy drop shadows / boxes-in-boxes.
- ✅ One brand accent for emphasis. ❌ Multiple competing accent colours.
- ✅ `ink-muted` for secondary text. ❌ Low-contrast grey-on-grey body copy.
- ✅ Tabular figures for aligned numbers. ❌ Proportional figures in tables.
- ✅ Reuse `@/components/ui`. ❌ Re-implement buttons/cards/badges inline.
- ❌ No `dark:` variants — light mode only (legacy `dark:` is being stripped).
