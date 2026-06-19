'use client';

import { useEffect } from 'react';

// Client-side belt-and-suspenders redirect for the rare case a visitor
// reaches this stub without the Cloudflare _redirects 301 firing (the
// edge rule is the primary path). Preserves the deep-link path beyond
// the moved root.
export function RedirectToStatus({ target }: { target: string }) {
  useEffect(() => {
    window.location.replace(target);
  }, [target]);
  return null;
}
