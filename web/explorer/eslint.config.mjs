// Flat ESLint config (ESLint 9+). Replaces the legacy .eslintrc.json — `next
// lint` was removed in Next 16, and eslintrc support is going away in ESLint 10.
// eslint-config-next 16 ships native flat-config arrays, so we spread them
// directly (no FlatCompat shim needed).
import coreWebVitals from 'eslint-config-next/core-web-vitals';
import typescript from 'eslint-config-next/typescript';

const eslintConfig = [
  { ignores: ['.next/**', 'out/**', 'node_modules/**', 'next-env.d.ts'] },
  ...coreWebVitals,
  ...typescript,
  {
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      // eslint-config-next 16 turns on the React Compiler react-hooks rules.
      // Enforced as ERRORS: every flagged site has been refactored to the
      // React Compiler-safe pattern (data-fetching effects → react-query,
      // reset-state effects → adjust-state-during-render, in-render impurity
      // → module helpers / effects). New violations must be fixed, not
      // downgraded.
      'react-hooks/set-state-in-effect': 'error',
      'react-hooks/static-components': 'error',
      'react-hooks/purity': 'error',
      'react-hooks/immutability': 'error',
      'react-hooks/refs': 'error',
    },
  },
];

export default eslintConfig;
