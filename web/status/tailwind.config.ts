import type { Config } from 'tailwindcss';

const config: Config = {
  content: ['./src/**/*.{js,ts,jsx,tsx,mdx}'],
  theme: {
    extend: {
      colors: {
        ink: { DEFAULT: '#0f172a', muted: '#475569', faint: '#94a3b8' },
        surface: { DEFAULT: '#ffffff', subtle: '#f8fafc', line: '#e2e8f0' },
        ok: { 50: '#ecfdf5', 500: '#10b981', 600: '#059669', 700: '#047857' },
        warn: { 50: '#fffbeb', 500: '#f59e0b', 600: '#d97706', 700: '#b45309' },
        bad: { 50: '#fef2f2', 500: '#ef4444', 600: '#dc2626', 700: '#b91c1c' },
        brand: { 500: '#0ea5e9', 600: '#0284c7' },
      },
      fontFamily: {
        sans: ['var(--font-sans)', 'system-ui', 'sans-serif'],
        mono: ['var(--font-mono)', 'ui-monospace', 'monospace'],
      },
    },
  },
  plugins: [],
};

export default config;
