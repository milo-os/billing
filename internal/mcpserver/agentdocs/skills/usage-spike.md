# Investigate a usage spike

Use when someone sees usage or spend jump, or asks when or why a meter
changed.

1. Identify the meter. If they name a resource type rather than a meter, call
   list_meters and pick the meter that measures it.
2. Call usage_daily for that meter over a range that covers the change with
   some days either side (from and to). Find the day or days where usage
   departs from its usual level.
3. Call usage_breakdown for the same meter with from and to around those
   days, by the dimension most likely to explain it: httproute_name (which
   ALB) or region for networking, model for the assistant, runtime_class for
   compute. If one value accounts for the jump, that is the source.
4. If activity tools are available, check what changed in the project on or
   just before the spike day: a new or changed resource often explains it.
5. Report when it started, how large it is compared with the usual level,
   what drives it, and whether it is still going on. Mention what it adds to
   spend only from usage_summary's figures.

A gauge meter (latest or peak) rising means more is allocated, not that more
was consumed that day; explain it that way.
