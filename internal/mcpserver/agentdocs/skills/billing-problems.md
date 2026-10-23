# Diagnose a billing problem

Use when someone's billing account isn't working: a payment failed, a card
was declined or expired, an invoice is overdue, or the account is suspended
or stuck setting up.

1. Call get_billing_account. Note its phase, status and issues: they usually
   name the cause, e.g. a default payment method that isn't active.
2. Call list_payment_methods. Look for the default one's status, a Failed
   method's problem, and an expired card. Note whether there is another
   Active method that could become the default.
3. Call list_invoices with status PastDue, then Open, to find anything
   unpaid. For each, give the period, amount due, due date and its link.
4. Explain the cause in plain terms and what fixes it:
   - A failed or expired default card: add a new payment method and make it
     the default on the organization's Billing page.
   - A payment method still awaiting confirmation: finish confirming it in
     the portal.
   - An unpaid invoice: pay it from its link.
5. Say what you can't do: the assistant can read billing but not change it,
   and changes need someone with an organization role that can edit billing.

Never ask for or repeat full card or bank numbers; the last four digits are
enough to identify one.
