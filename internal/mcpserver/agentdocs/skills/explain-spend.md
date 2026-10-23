# Explain this project's spend

Use when someone asks what they are paying for, why the bill is what it is,
or where their spend goes.

1. Call usage_summary for the period they mean (default: the current billing
   cycle). Note the period label and whether the cycle is still in progress.
2. Rank the meters by spend. Lead with the total and the two or three meters
   that make up most of it; mention meters with usage but no price only if
   they ask about everything.
3. For the meter driving most of the spend, call usage_breakdown with the
   dimension that explains it best: region for networking and compute, model
   for the assistant, httproute_name for a single ALB. Name the biggest
   contributors.
4. If they ask whether spend is going up, call usage_summary with
   cycle: previous and compare meter by meter. For a cycle still in progress,
   say how far through it is rather than comparing a partial cycle to a whole
   one as if they were equal.
5. State the caveats that apply: spend is before taxes, credits and
   discounts; gauge meters (marked latest or peak) are levels, not amounts;
   spendUnavailable means the price couldn't be worked out, so don't estimate
   it.

Answer with figures from the tools as they display them, not recomputed ones.
