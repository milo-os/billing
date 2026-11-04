import { rollUpByService, serviceName, usageAccounts, usageWindows } from './usage';
import { describe, expect, test } from 'bun:test';

describe('serviceName', () => {
  test('names a meter after its domain, like internal/usagereport', () => {
    expect(serviceName('compute.datumapis.com/instance/cpu-seconds')).toBe('Compute');
    expect(serviceName('dns.networking.miloapis.com/queries')).toBe('DNS');
    expect(serviceName('ai-gateway.datumapis.com/tokens')).toBe('AI Gateway');
    expect(serviceName('')).toBe('Other');
  });
});

describe('usageWindows', () => {
  test('compares this month so far with the same point last month', () => {
    const w = usageWindows(new Date('2026-11-15T12:00:00Z'));
    expect(w.start.toISOString()).toBe('2026-11-01T00:00:00.000Z');
    expect(w.previousStart.toISOString()).toBe('2026-10-01T00:00:00.000Z');
    expect(w.previousEnd.toISOString()).toBe('2026-10-15T12:00:00.000Z');
  });

  test('clamps to the end of a shorter previous month', () => {
    const w = usageWindows(new Date('2026-03-31T12:00:00Z'));
    expect(w.previousEnd.toISOString()).toBe('2026-03-01T00:00:00.000Z');
  });
});

describe('rollUpByService', () => {
  test('sums meter spend per service across batches', () => {
    const byService = rollUpByService([
      { status: { meters: [{ meterName: 'compute.datumapis.com/a', spend: { amount: '1.5' } }] } },
      {
        status: {
          meters: [
            { meterName: 'compute.datumapis.com/b', spend: { amount: '2' } },
            { meterName: 'alb.datumapis.com/requests' },
          ],
        },
      },
    ]);
    expect(byService.get('Compute')?.spend).toBe(3.5);
    expect(byService.get('ALB')?.spend).toBe(0);
  });

  test('tells usage priced at $0 apart from no usage', () => {
    // As on staging in November 2026: a 100% offer discount prices real usage at $0.
    const byService = rollUpByService([
      {
        status: {
          meters: [
            {
              meterName: 'compute.datumapis.com/instance/milli-cpu-seconds',
              spend: { amount: '0' },
              groupings: [{ series: [{ total: '1022000000' }] }],
            },
            {
              meterName: 'alb.datumapis.com/requests',
              spend: { amount: '0' },
              groupings: [{ series: [{ total: '0' }] }],
            },
          ],
        },
      },
    ]);
    expect(byService.get('Compute')).toEqual({ spend: 0, hasUsage: true });
    expect(byService.get('ALB')).toEqual({ spend: 0, hasUsage: false });
  });
});

describe('usageAccounts', () => {
  test('only queries live accounts that have projects', () => {
    const picked = usageAccounts([
      { metadata: { name: 'a' }, status: { phase: 'Ready', linkedProjectsCount: 2 } },
      { metadata: { name: 'b' }, status: { phase: 'Ready', linkedProjectsCount: 0 } },
      { metadata: { name: 'c' }, status: { phase: 'Archived', linkedProjectsCount: 1 } },
    ]);
    expect(picked.map((a) => a.metadata?.name)).toEqual(['a']);
  });
});
