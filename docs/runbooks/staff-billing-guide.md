# Billing Guide for Staff

How to use billing in the staff portal: prices, offers and discounts,
and how to onboard a new customer, including one on a contract.

This guide is for staff, not engineers. Every amount in it is made up.

- [The pieces](#the-pieces)
- [Where to find things](#where-to-find-things)
- [Prices](#prices)
- [Offers](#offers)
- [Discounts](#discounts)
- [Putting an account on an offer](#putting-an-account-on-an-offer)
- [Onboarding a new customer](#onboarding-a-new-customer)
- [Setting up a contract](#setting-up-a-contract)
- [Previewing the first invoice](#previewing-the-first-invoice)
- [Changing a contract](#changing-a-contract)
- [What happens automatically](#what-happens-automatically)
- [What the customer sees](#what-the-customer-sees)
- [When something looks wrong](#when-something-looks-wrong)
- [Further reading](#further-reading)

## The pieces

| Term | What it means |
|---|---|
| **Billing account** | Who pays. Belongs to an organization, and its projects are billed to it. The customer creates it in the cloud portal when they sign up |
| **Price** (ServicePricing) | One price: a rate for a kind of usage, such as vCPU time, or a fixed fee, charged once or monthly. A price never changes once it exists. A new price is a new one, such as `compute-vcpu-v2` |
| **Offer** | A named bundle of prices, optionally with discounts. Every billing account is on exactly one Offer |
| **Default offer** | The Offer new billing accounts are put on automatically |
| **Discount** | A percentage off, set on an Offer, for all of it or for some prices in it |
| **Payment terms** | Lets an account pay by invoice instead of card, with a number of days to pay |
| **Contract** | A customer's own Offer with a start date: fees agreed with them, and optionally usage at list or agreed rates. Billed on the same day each month |
| **Invoice** | What the customer is asked to pay. Created and sent by our billing provider (OpenMeter, sending through Stripe), never by hand |

## Where to find things

In the staff portal:

- **Finance → Billing Accounts** lists every billing account. Open one to see
  its dashboard: usage, invoices, projects, payment, contract and offer. The
  **Actions** menu at the top has the billing changes you can make:
  - **Grant payment terms** (or **Change payment terms**, or **Review terms
    application** when the customer has applied);
  - **Switch offer**;
  - **Set up contract** (or **Change contract fees** once it has one).
- **Finance → Billing Accounts → Applications** is the queue of payment terms
  applications, with a count of those waiting for a decision.
- **Service Catalog** lists the platform's services. A service's page has a
  **Pricing** card with its prices, every rate and tier.
- **Offers** is next to Service Catalog: use the switcher in the page title
  (breadcrumb) on Service Catalog to get there. Contract offers are hidden
  from the list unless you click **Show contract offers**.

If an action is missing or saving fails with a permission error, ask in the
billing team's channel for the role you need.

## Prices

**List prices aren't created in the staff portal.** The platform's prices are
written by each service's team in that service's repository, and go live
after review, the same way the service's meters do. Staging and production
have separate prices. To add or change a list price, ask the service's owners.

You can see every price, read-only, on the service's **Pricing** card in
**Service Catalog**.

A price can't be edited once it exists. Changing a price means a new price
(for example `compute-vcpu-v2`) and then a new Offer that uses it (see
[Raising prices for everyone](#raising-prices-for-everyone)). Customers
already on the old Offer keep the old price until you move them.

The prices you *do* create are a contract's own: its fees, and a customer's
agreed rates. The contract page creates those for you (see
[Setting up a contract](#setting-up-a-contract)).

## Offers

### Creating an Offer

1. Go to **Offers** and click **Create Offer**.
2. Give it an **ID**, such as `payg-v7` or `payg-example-co`. It can't be
   changed later. Lowercase letters, numbers and dashes only.
3. Give it a **Display name**, such as "Pay as you go".
4. Pick the **Service pricings** it includes. An Offer can have only one
   price for each kind of usage: you can't include `compute-vcpu-v1` and
   `compute-vcpu-v2` together.
5. Tick the **Charge types** the prices need: **Usage** for usage prices,
   **One-time** and **Recurring** for fees. Every price you picked must be
   covered by a ticked charge type, or the Offer won't publish properly.
6. Add any [discounts](#discounts).
7. Click **Create draft**.

A draft does nothing yet. You can change its prices, charge types and
discounts on its page under **Draft configuration**, then **Save draft**.

### Publishing an Offer

On the draft's page, click **Publish**. Publishing copies the prices and
discounts into the Offer as they are at that moment, and from then on they
can't change: only the display name can be edited. To change anything else,
create a new Offer.

Only published Offers can be assigned to accounts or set as the default.
Right after publishing, the page may say **Pending snapshot** for a few
seconds while the prices are copied in.

### Setting the default offer

On a published Offer's page, click **Set as default**. New billing accounts,
and any account without an Offer, are put on it automatically.

The confirmation has a checkbox, **Also move accounts currently on the
previous default to this Offer**. Tick it when the new Offer replaces the old
one for everyone. Leave it unticked to change only what new accounts get.

### Raising prices for everyone

1. Ask the service's owners for the new prices (see [Prices](#prices)).
2. Create a new Offer, such as `payg-v7`, with the new prices.
3. Publish it.
4. Set it as the default, with **Also move accounts…** ticked.

Accounts on their own Offers, such as those with negotiated discounts or
contracts, aren't moved. Give them new Offers of their own if they should get
the new prices.

## Discounts

A discount is a whole-number percentage off, from 0 to 100, set on an Offer:

- **General discount** applies to every price in the Offer that doesn't have
  its own.
- **Per-service discounts** override the general discount for one price.
  Leave it empty to use the general discount.

The discount is taken off the whole charge for the month, after the rates are
applied. A customer who used $400 of vCPU with 25% off pays $300. The invoice
shows the list price, the discount on its own line, and what's left, so the
customer can see what they saved.

Discounts are set while the Offer is a draft and are fixed once it's
published.

### Giving one customer a discount

1. Create an Offer for them with the same prices as the default offer. Name
   it after them, such as `payg-v7-example-co`.
2. Set the discount, for example 15% general.
3. Publish it.
4. On their billing account, use **Switch offer** to move them onto it.

The discount applies from now: their next invoice reflects it.

### Ending a discount

Discounts don't expire on their own. To end one, switch the account back to
the default offer (or to another Offer). If a discount is meant to last a set
time, put a reminder in your calendar to switch the account back.

### Discounts on a contract

On a contract, each fee and each kind of usage from the price list has its
own **% off** (see [Setting up a contract](#setting-up-a-contract)).

## Putting an account on an offer

On the billing account, open **Actions → Switch offer**, pick a published
Offer and click **Switch**. The account moves straight away: what it uses
from now is billed at the new Offer's prices, and its next invoice reflects
the change.

The list shows published Offers only, and leaves out other customers'
contract offers, which belong to one account each.

Don't use **Switch offer** on an account with a contract. It would replace
the contract's fees. Use **Change contract fees** instead (see
[Changing a contract](#changing-a-contract)).

## Onboarding a new customer

Every customer starts the same way: they sign up in the cloud portal, create
their organization, add their contact details and create a billing account.
The billing account is put on the default offer automatically, and their
projects are billed to it. What happens next depends on how they pay.

### Customers who pay by card

Nothing to do. They add a card in the cloud portal, and each month's invoice
is charged to the card automatically.

### Customers who pay by invoice

The customer creates their billing account but doesn't add a card. Then
either:

- **You grant payment terms directly**, for example after a sales call. On
  the billing account, open **Actions → Grant payment terms**.
- **They apply in the cloud portal**, and you review the application under
  **Applications**. The review page shows what they asked for, their fraud check,
  their situation in their own words and the account's current terms.
  **Approve** grants the terms. **Decline** asks for a reason, a message to
  the customer and internal notes the customer never sees. If the account
  already has terms with no end date, **Approve** is greyed out: give those
  terms an end date first.

The payment terms form asks for:

| Field | What to enter |
|---|---|
| **Net days** | Days after the invoice date that payment is due, such as 30 |
| **Accounts payable email** | Where the customer wants invoices sent |
| **PO number** | The customer's purchase order number, if they have one. Printed on every invoice |
| **Agreement reference** | A signed MSA or order form, if there is one. Otherwise our standard terms apply. Printed on every invoice |
| **Start date** | Leave empty to start now |
| **End date** | Leave empty for terms with no fixed end |
| **Reason (internal)** | Why the terms were granted. Never shown to the customer |

Once the terms are active, the account's header says **Billed by invoice**.
Each month an invoice is emailed to the customer, payable within the net
days, by bank transfer.

To change the terms later, use **Actions → Change payment terms**. To stop
them, use **End** (or **Withdraw**, for terms that haven't started) on the
Payment card. The account then needs a card to keep using paid services.

Only one set of terms can be active at a time: end the current terms before
granting new ones that overlap.

### Customers on a contract

A contract customer, such as a bare metal customer, has agreed fees and a
start date. Usually they pay by invoice. Here's the whole flow, using a
made-up customer, Example Co:

| | Agreed |
|---|---|
| Setup fee | $2,000, once |
| Dedicated servers | 4 at $800 each a month |
| Cross connects | 2 ports at $120 a port a month (list price $150) |
| Usage | Billed at list prices |
| Start | 1 December |
| Payment | Net 30, PO 4500012345 |

1. **The customer signs up** and creates their billing account in the cloud
   portal, as above. You need their billing account before you can do
   anything else.
2. **Grant payment terms**, with the PO number and agreement reference from
   the contract (see [Customers who pay by invoice](#customers-who-pay-by-invoice)).
3. **Set up the contract**, with its fees, what it bills from the price list
   and its start date (see [Setting up a contract](#setting-up-a-contract)).
4. **Preview the first invoice** and compare it with the contract (see
   [Previewing the first invoice](#previewing-the-first-invoice)). For Example
   Co it should show $2,000 + $3,200 + $240 = **$5,440**, before tax.
5. **On 1 December, the contract starts on its own.** The first invoice is
   created and sent to the customer a few minutes later. You don't send it.
6. **Check the first invoice** on the account's Contract card, under
   **Already invoiced**.

After that, Example Co is invoiced on the 1st of each month: $3,440 for the
next month's fees, plus the previous month's usage.

An existing customer can move onto a contract the same way. Until the start
date they're billed as before, on their old Offer. On the start date they move
onto the contract.

## Setting up a contract

On the billing account, open **Actions → Set up contract**. The contract page
has four parts.

### Contract fees

Fees made for this contract, such as a setup fee or a negotiated monthly fee.
Each is a line on the invoice. The page starts with a blank **Setup fee** and
**Monthly fee**: fill them in, remove them, or **Add fee** for more. A
contract can have up to 20 fees, counting those from the price list.

| Field | What to enter |
|---|---|
| **Name** | What the invoice calls it, such as "Setup fee" or "Dedicated server" |
| **Amount (USD)** | The price of one, such as `800` or `800.00` |
| **Quantity** | How many, from 1 to 10,000. Four servers at $800 is one fee with quantity 4, charged $3,200 |
| **Charged** | **Once** (charged at the start) or **Monthly** |
| **Per** | Optional unit the amount is per, such as `port` or `kW`. Only a label: the invoice then reads "Cross connect: 2 port × $120.00" instead of "Cross connect × 2" |
| **Service** | Optional. Pick one only if the fee is for a particular service. Setup, support and rack fees usually aren't |

### Price list

What the contract bills from the platform's prices: usage, and any fees the
platform lists, such as a server type or a cross connect. A new contract
starts with the default offer's usage ticked. Untick everything for a
contract that charges only its fees.

For each row you tick:

- **Qty**, for a fee: how many.
- **% off**: a discount on that row.
- **Customer price**, or **Customer rates** for usage: the customer's agreed
  price, if it differs from the list price. Leave it empty to bill the list
  price.

When you enter a customer price, the page creates a price of their own,
linked to the list price, so the list price is always shown beside it, on
this page and on the account's Contract card.

Use **% off** when the customer should see list prices with a discount on
their invoices. Use a customer price when the agreed price should appear as
the price itself.

Where a service has several versions of a usage price, they're listed newest
first. Pick the one the contract should use.

### Start date

The day the contract's charges start, in UTC, from today up to a year ahead.
It can't be in the past: charges can't be backdated. A contract signed before
it's entered starts on the day you enter it, or on a later date such as when
the hardware is handed over.

The start date is also the billing day: a contract starting on the 18th bills
on the 18th of each month. One starting on the 29th, 30th or 31st bills on
the last day of shorter months.

**Check the date before saving.** Once set, a contract's start date can't be
moved and the contract can't be removed.

A contract starting today is charged from the moment you save, so its first
month is a part month, charged less. The summary warns you. Start tomorrow
for a full first month.

### Summary

The summary on the right adds it all up as you type: the fees, the usage
billed, and what the first invoice and each month after will come to, before
discounts and tax, plus any usage.

Click **Set up contract** to save. The portal creates the contract's prices
and its Offer, and moves the account onto it from the start date. The
account's dashboard then shows a **Contract** card with the start date,
billing day and fees.

## Previewing the first invoice

**You can't send a contract's first invoice yourself.** It's sent
automatically when the contract starts. What you can do is preview it
beforehand, and fix the contract if it's wrong.

On the Contract card, click **Preview first invoice**. The billing provider
works out the invoice from the contract and shows each line and the **Total
before tax**. Nothing is created and nothing is sent to the customer.

The preview covers the contract's fees only: the setup fee and the first
month. Usage is never on a preview. It's billed after the month it's used in,
so the first invoice has none anyway.

If the preview is wrong, use **Change fees** on the Contract card before the
start date. Until the contract starts, everything can be changed, setup fees
included. If the start date is wrong, ask the billing team before the
contract starts.

After the contract starts, the button becomes **Preview next invoice** and
shows the fees due on the next billing date.

## Changing a contract

On the billing account, open **Actions → Change contract fees**, or click
**Change fees** on the Contract card. The same page opens, filled in with the
contract as it is. Change the fees, quantities, price list rows, discounts or
customer prices, then click **Change fees**.

- **Before the contract starts**, the changes replace what was set up. The
  start date can't be changed.
- **After it starts**, the new fees apply from the next billing date, so the
  current month is billed as agreed. The Contract card shows **New fees take
  effect on** that date.
- **One-time fees aren't charged again.** Once the contract has started, its
  setup fee has been charged, so the page leaves it out and won't let you add
  a one-time fee. A one-off charge in the middle of a contract can't be made
  from the staff portal yet: ask the billing team.

Each change creates a new Offer for the contract. Don't edit or reuse a
contract's Offers directly.

## What happens automatically

| When | What happens | Who does it |
|---|---|---|
| A customer creates a billing account | It's put on the default offer, and their projects are billed to it | Billing |
| You save a contract | Its prices and Offer are created, and the account is set to move onto it at the start date | Staff portal |
| Until the start date | The account is billed as before | OpenMeter |
| The contract's start date | The account moves onto the contract. The first invoice (setup fee and first month) is created and sent a few minutes later | OpenMeter |
| Each billing date | An invoice for the next month's fees and the last month's usage, with discounts applied | OpenMeter |
| An invoice is issued | Card accounts are charged. Invoice accounts are emailed the invoice, payable within their net days | Stripe |
| You change a started contract | The new fees start on the next billing date | OpenMeter |

You never create or send invoices by hand.

## What the customer sees

In the cloud portal, on their billing page:

- **Contract**: the start date, the billing day, and each fee with its
  quantity, such as "4 × $800.00" or "2 port × $120.00". They see it as soon
  as the contract is set up, before it starts. Fees are shown before
  discounts. A changed contract shows the new fees straight away, even
  though they're billed from the next billing date.
- **Payment terms**: net days, PO number and agreement reference.
- **Discounts**: the percentage off each service, while there are any.
- **Usage and invoices**: once the contract has started, usage is grouped by
  the contract's billing day, not the 1st.

Invoices are emailed to them and listed in the cloud portal. On an invoice:

- each fee is a line, named with its quantity;
- a discount is its own line, after the list price;
- a customer price appears as the price itself;
- the PO number and agreement reference are printed on it.

Customers don't see Offer names, list prices behind their customer prices, or
your internal notes and reasons.

## When something looks wrong

| What you see | What it means |
|---|---|
| **Set up contract** is greyed out | The account has no Offer yet. It gets the default offer automatically, so try again in a minute. If it doesn't, ask the billing team |
| "A contract can't start in the past" | Pick today or a later date |
| "The contract has started, so a one-time fee would be charged again" | You added a one-time fee to a started contract. Remove it |
| The preview says "There's nothing to invoice yet" | Everything due has already been invoiced. Previews don't include usage |
| "The first invoice hasn't come through yet" | It arrives a few minutes after the contract starts. If it hasn't after an hour, ask the billing team |
| A published Offer has no **Published at** date after a few minutes | Its charge types probably don't cover every price in it. Create a new Offer with the right charge types |
| A customer says their invoice is wrong | Compare it with the Contract card and a preview, then ask the billing team. Don't edit anything in Stripe or OpenMeter |

## Further reading

For the detail behind this guide:

- [Contract billing](../enhancements/contract-billing.md)
- [Offer discounts](../enhancements/offer-discounts.md)
- [Invoicing](../enhancements/invoicing.md)
- [Billing arrangements (payment terms)](../enhancements/billing-arrangements.md)
