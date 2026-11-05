# ui/provider — portal plugin conventions

`ui/provider` is billing's staff-portal plugin: a Module Federation remote
rendered directly into staff-portal's DOM at runtime. Two things follow from
that which aren't obvious from its own source:

## Prefer datum-ui components over hand-rolled ones

Before writing a custom component (tabs, tables, empty states, code/YAML
viewers, stat rows, etc.), check `datum-ui/packages/datum-ui/src/components/`
(base/ and features/) for an existing one — `tabs`, `table`,
`empty-content` (the real "coming soon"/empty-state component), and
`code-editor` (read-only YAML/JSON display) all already exist there and
should be used instead of reimplementing the same thing with plain divs.

Only import `@datum-cloud/datum-ui/<subpath>` names that the target host's
`federation-host.ts` actually lists in its shared config — cloud-portal and
staff-portal each curate their own subset, and it differs between them.
Anything outside that list still works, it just bundles its own copy in the
plugin's remote instead of sharing the host's singleton instance.

## The plugin has no Tailwind build of its own

There's no `tailwind.config`/`postcss.config`/`tailwindcss` package in
`ui/provider`, and no CSS file of its own — a plugin's
JSX renders into the *host's* DOM, so a Tailwind class only takes visual
effect if that exact class string already happens to exist in the host's own
compiled CSS. Common/simple utilities (`flex`, `grid`, `gap-3`, `grid-cols-2`,
etc.) usually coincidentally exist there because most real apps use them
somewhere. **Arbitrary-value classes almost never do** —
`grid-cols-[minmax(0,1fr)_90px]`, `min-w-[200px]`, `tracking-[0.03em]`,
`max-h-[600px]`, etc. — and they fail *silently*: no error, the class is just
absent from the host's stylesheet, so the layout collapses or the style is
simply missing.

**Use datum-ui components first** (their own classes ship in datum-ui's
compiled CSS, so they're guaranteed to work). If a genuinely custom layout is
unavoidable, use an inline `style={{...}}` for anything beyond a handful of
extremely common utility classes — don't reach for a bracketed arbitrary
value.

One exception: a class string copied verbatim from staff-portal's own source
is in its CSS by construction. `StatusBadge` (`src/components/parts.tsx`)
uses staff-portal's `STATUS_BADGE_CLASS` (`app/components/badge/badge-tokens.ts`
there), arbitrary values included, so status chips match the host's. If that
constant changes in staff-portal, change it here too.

## Keep the money rules in step with staff-portal

`src/lib/invoices.ts` mirrors staff-portal's per-account rules (stale
invoices, phase defaults, amount owed, aging). A fleet total must be the sum
of what each account page shows, so change both together.
