import type { Config } from 'tailwindcss';

const config: Config = {
  // Manual toggle via `class="dark"` on <html>. The inline init
  // script in layout.tsx hydrates the class from localStorage
  // before the first paint; absent that, it falls back to the
  // OS prefers-color-scheme media query so the default behaviour
  // matches what shipped before the toggle.
  darkMode: 'class',
  content: ['./src/**/*.{js,ts,jsx,tsx,mdx}'],
  theme: {
    extend: {
      // Brand palette is intentionally minimal at v1.
      // A real design pass replaces this in Phase 7.
      colors: {
        brand: {
          50: '#f0f9ff',
          100: '#e0f2fe',
          500: '#0ea5e9',
          600: '#0284c7',
          900: '#0c4a6e',
        },
        // Semantic colours for delta strips, etc.
        up: {
          subtle: '#bbf7d0',
          DEFAULT: '#16a34a',
          strong: '#15803d',
        },
        down: {
          subtle: '#fecaca',
          DEFAULT: '#dc2626',
          strong: '#b91c1c',
        },
        // Off-tone overlay for time-machine "viewing as of" mode.
        timepin: {
          DEFAULT: '#fef3c7',
          ring: '#f59e0b',
        },
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
