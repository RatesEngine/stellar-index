'use client';

import { useEffect, useState } from 'react';

import { AccountView } from '../AccountView';

// Reads the real account ID (G-strkey) from the path at runtime. G-strkeys are
// case-sensitive base32 — we never lowercase. The CF Function serves the one
// built shell for any /accounts/{g}; richlist/named accounts get pre-rendered
// + indexed later (SEO plan D6).
export function AccountPathView() {
  const [id, setId] = useState('');
  useEffect(() => {
    const seg =
      window.location.pathname.replace(/\/+$/, '').split('/').pop() ?? '';
    setId(decodeURIComponent(seg));
  }, []);
  return <AccountView id={id} />;
}
