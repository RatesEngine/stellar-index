'use client';

import { useEffect, useState } from 'react';

import { ContractView } from '../../contract/ContractView';

// Reads the real contract ID (C-strkey, case-sensitive) from the path at
// runtime. The CF Function serves the one built shell for any /contracts/{id};
// active contracts get pre-rendered + indexed later (SEO plan D6).
export function ContractPathView() {
  const [id, setId] = useState('');
  useEffect(() => {
    const seg =
      window.location.pathname.replace(/\/+$/, '').split('/').pop() ?? '';
    setId(decodeURIComponent(seg));
  }, []);
  return <ContractView id={id} />;
}
