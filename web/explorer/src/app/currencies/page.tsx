import type { Metadata } from 'next';

import { CurrenciesView } from './CurrenciesView';

export const metadata: Metadata = {
  title: 'Currencies — fiat forex coverage',
  description:
    'World fiat currencies — live USD-base rates from currency-api (ECB / FRBNY-aggregated). Ticker, name, USD-denominated rate, inverse rate. Source: /v1/currencies.',
};

export default function CurrenciesPage() {
  return <CurrenciesView />;
}
