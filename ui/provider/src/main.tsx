/**
 * Standalone preview only; staff-portal never loads this. It wraps the page
 * in the router and QueryClient the host provides in production, at the
 * same path the host mounts the overview on.
 *
 * Without the staff-portal proxy, data calls fail and the page shows its
 * error state. Add `?fixtures` to answer them from a made-up fleet instead
 * (src/lib/fixtures.ts), which is how to look at the layout locally.
 */
import { buildFixtures, fixtureUsage } from './lib/fixtures';
import { DAY_MS } from './lib/invoices';
import FleetOverview from './pages/fleet-overview';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter, Route, Routes } from 'react-router';

const SERVICE = 'billing-miloapis-com';

function installFixtures() {
  const fixtures = buildFixtures(new Date());
  const lists: Record<string, unknown[]> = {
    billingaccounts: fixtures.accounts,
    invoices: fixtures.invoices,
    paymentmethods: fixtures.paymentMethods,
    billingarrangementapplications: fixtures.applications,
    billingentitlements: fixtures.entitlements,
    offers: fixtures.offers,
    creditgrants: fixtures.creditGrants,
    organizations: fixtures.organizations,
  };
  const json = (data: unknown) =>
    new Response(JSON.stringify({ code: 200, data }), {
      headers: { 'Content-Type': 'application/json' },
    });
  const realFetch = window.fetch.bind(window);

  window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === 'string' ? input : (input as Request).url, location.href);
    if (!url.pathname.startsWith('/api/internal/')) return realFetch(input, init);
    await new Promise((r) => setTimeout(r, 300));

    if (url.pathname.endsWith('/usagequeries')) {
      const body = JSON.parse(String(init?.body ?? '{}'));
      const days =
        (Date.parse(body.spec.endTime) - Date.parse(body.spec.startTime)) / DAY_MS;
      const previous = Date.parse(body.spec.endTime) < Date.now() - DAY_MS;
      return json(fixtureUsage(body.spec.billingAccounts.length, days, previous));
    }
    const balance = url.pathname.match(/billingaccountbalances\/([^/]+)$/);
    if (balance) {
      const amount = (balance[1].length * 731.37) % 9000;
      return json({ status: { currency: 'USD', forecast: { estimatedSpend: amount.toFixed(2) } } });
    }
    const resource = url.pathname.split('/').pop() ?? '';
    return json({ items: lists[resource] ?? [], metadata: {} });
  }) as typeof fetch;
}

if (new URLSearchParams(location.search).has('fixtures')) installFixtures();

const queryClient = new QueryClient();

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <div style={{ maxWidth: 1200, margin: '1rem auto', padding: '0 1rem' }}>
        <MemoryRouter initialEntries={[`/admin/service-catalog/${SERVICE}`]}>
          <Routes>
            <Route path="/admin/service-catalog/:name" element={<FleetOverview />} />
          </Routes>
        </MemoryRouter>
      </div>
    </QueryClientProvider>
  </StrictMode>
);
