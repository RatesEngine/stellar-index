import type { Config } from 'tailwindcss';

// ─────────────────────────────────────────────────────────────────────────
// Stellar Index design system — v2 (2026-06-17 redesign).
//
// Light mode only. Modern, minimal, tech-forward: an Inter type system on a
// near-white canvas, hairline borders over heavy shadows, generous
// whitespace, and one confident blue accent. The full token set is
// documented in docs/architecture/design-system.md and shown live at
// /dev/primitives (the style guide).
//
// Dark mode is intentionally OFF — `darkMode: 'class'` is retained only so
// the legacy `dark:` variants still compile while we strip them; the `dark`
// class is never applied, so they never activate.
// ─────────────────────────────────────────────────────────────────────────
const config: Config = {
  darkMode: 'class',
  content: ['./src/**/*.{js,ts,jsx,tsx,mdx}'],
  theme: {
    extend: {
      colors: {
        // ─── Brand — a confident, modern tech blue (the single accent) ───
        brand: {
          50: '#eef4ff',
          100: '#d9e6ff',
          200: '#bcd3ff',
          300: '#8eb6ff',
          400: '#598dff',
          500: '#3366f6',
          600: '#1f4ae0', // primary action / link
          700: '#1a3bc2',
          800: '#1b349c',
          900: '#1c317b',
          950: '#151f4b',
        },
        // ─── Surfaces (light-mode layering) ───
        // canvas = page background; DEFAULT = elevated card; muted/subtle =
        // recessed wells, table headers, hover rows.
        surface: {
          DEFAULT: '#ffffff',
          canvas: '#fbfcfe',
          muted: '#f6f8fb',
          subtle: '#eef2f7',
        },
        // ─── Lines — hairline borders (we lean on these, not shadows) ───
        line: {
          subtle: '#eef1f5',
          DEFAULT: '#e3e8ef',
          strong: '#cdd5e0',
        },
        // ─── Ink — the text scale ───
        ink: {
          DEFAULT: '#0b1220', // headings / strongest body
          strong: '#0b1220',
          body: '#37415b', // default paragraph text
          muted: '#64748b', // secondary / labels
          faint: '#94a3b8', // captions / disabled / placeholders
        },
        // ─── Semantic deltas (price up/down) ───
        up: {
          subtle: '#dcfce7',
          DEFAULT: '#16a34a',
          strong: '#15803d',
        },
        down: {
          subtle: '#fee2e2',
          DEFAULT: '#dc2626',
          strong: '#b91c1c',
        },
        // ─── Status / severity (banners, incidents, degraded modes) ───
        bad: {
          50: '#fef2f2',
          300: '#fca5a5',
          500: '#ef4444',
          700: '#b91c1c',
          900: '#7f1d1d',
        },
        warn: {
          50: '#fffbeb',
          300: '#fcd34d',
          500: '#f59e0b',
          700: '#b45309',
          900: '#78350f',
        },
        ok: {
          50: '#f0fdf4',
          300: '#86efac',
          500: '#22c55e',
          700: '#15803d',
        },
        // Off-tone overlay for time-machine "viewing as of" mode.
        timepin: {
          DEFAULT: '#fef3c7',
          ring: '#f59e0b',
        },
      },
      fontFamily: {
        sans: ['var(--font-sans)', 'system-ui', 'sans-serif'],
        mono: ['var(--font-mono)', 'ui-monospace', 'SFMono-Regular', 'monospace'],
      },
      fontSize: {
        // Tighter display sizes with set line-heights + tracking for headings.
        display: ['3.5rem', { lineHeight: '1.05', letterSpacing: '-0.035em' }],
        'display-sm': ['2.5rem', { lineHeight: '1.1', letterSpacing: '-0.03em' }],
        h1: ['2rem', { lineHeight: '1.15', letterSpacing: '-0.025em' }],
        h2: ['1.5rem', { lineHeight: '1.2', letterSpacing: '-0.02em' }],
        h3: ['1.25rem', { lineHeight: '1.3', letterSpacing: '-0.015em' }],
      },
      letterSpacing: {
        tightest: '-0.04em',
      },
      borderRadius: {
        card: '0.875rem', // 14px — the default card/panel radius
        '4xl': '2rem',
      },
      boxShadow: {
        // Soft, low-opacity, cool-tinted — minimal elevation over hairlines.
        xs: '0 1px 2px 0 rgb(11 18 32 / 0.04)',
        card: '0 1px 2px rgb(11 18 32 / 0.04), 0 1px 3px rgb(11 18 32 / 0.05)',
        elevated:
          '0 8px 24px -6px rgb(11 18 32 / 0.10), 0 2px 6px -2px rgb(11 18 32 / 0.05)',
        focus: '0 0 0 3px rgb(31 74 224 / 0.20)',
      },
      maxWidth: {
        page: '80rem', // 1280px — the standard content frame
        prose: '46rem',
      },
      keyframes: {
        'fade-in': {
          from: { opacity: '0', transform: 'translateY(4px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
      },
      animation: {
        'fade-in': 'fade-in 0.3s ease-out both',
      },
    },
  },
  plugins: [],
};

export default config;
