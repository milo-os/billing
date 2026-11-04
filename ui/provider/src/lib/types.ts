/**
 * The fields of billing's resources this plugin reads, mirroring
 * api/v1alpha1 and api/usage/v1alpha1 in this repo. Everything is optional:
 * the page must survive a resource the provider hasn't finished writing.
 * Amounts are decimal strings.
 */

export interface ObjectMeta {
  name?: string;
  namespace?: string;
  uid?: string;
  creationTimestamp?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

export interface Condition {
  type: string;
  status: 'True' | 'False' | 'Unknown';
  reason?: string;
  message?: string;
}

export interface BillingAccountRef {
  name?: string;
}

export interface BillingAccount {
  metadata?: ObjectMeta;
  spec?: {
    currencyCode?: string;
    paymentTerms?: { netDays?: number; invoiceFrequency?: string; invoiceDayOfMonth?: number };
    contactInfo?: { email?: string; name?: string; businessName?: string };
    defaultPaymentMethodRef?: { name?: string };
  };
  status?: {
    phase?: 'Provisioning' | 'Ready' | 'Suspended' | 'Archived';
    conditions?: Condition[];
    linkedProjectsCount?: number;
    paymentArrangement?: {
      name?: string;
      type?: string;
      startsAt?: string;
      endsAt?: string;
      invoice?: { netDays?: number; creditLimit?: string };
    };
  };
}

export type InvoicePhase = 'Open' | 'Paid' | 'PastDue' | 'Void';

export interface Invoice {
  metadata?: ObjectMeta;
  spec?: {
    billingAccountRef?: BillingAccountRef;
    period?: { start?: string; end?: string };
  };
  status?: {
    phase?: InvoicePhase;
    currencyCode?: string;
    total?: string;
    amountPaid?: string;
    amountDue?: string;
    creditsApplied?: string;
    dueDate?: string;
    paidAt?: string;
  };
}

export interface PaymentMethod {
  metadata?: ObjectMeta;
  spec?: { billingAccountRef?: BillingAccountRef; displayName?: string };
  status?: {
    phase?: 'Pending' | 'AwaitingConfirmation' | 'Active' | 'Failed';
    details?: {
      type?: string;
      card?: { brand?: string; last4?: string; expiryMonth?: number; expiryYear?: number };
    };
    failureReason?: string;
  };
}

export interface BillingArrangement {
  metadata?: ObjectMeta;
  spec?: {
    billingAccountRef?: BillingAccountRef;
    type?: string;
    startsAt?: string;
    endsAt?: string;
    invoice?: { netDays?: number; creditLimit?: string };
  };
  status?: { phase?: 'Scheduled' | 'Active' | 'Ended' };
}

export interface BillingArrangementApplication {
  metadata?: ObjectMeta;
  spec?: {
    billingAccountRef?: BillingAccountRef;
    organization?: { legalName?: string };
    invoice?: { estimatedMonthlySpend?: string };
  };
  status?: { phase?: 'Pending' | 'Approved' | 'Declined' | 'Withdrawn' };
}

export interface BillingEntitlement {
  metadata?: ObjectMeta;
  spec?: { billingAccountRef?: BillingAccountRef; offerRef?: { name?: string } };
}

export interface Offer {
  metadata?: ObjectMeta;
  spec?: { displayName?: string };
}

export interface CreditGrant {
  metadata?: ObjectMeta;
  spec?: {
    billingAccountRef?: BillingAccountRef;
    amount?: string;
    currency?: string;
    reason?: string;
    voided?: boolean;
  };
  status?: {
    phase?: 'Pending' | 'Active' | 'Expired' | 'Voided' | 'Failed';
    expiresAt?: string;
  };
}

export interface Organization {
  metadata?: ObjectMeta;
}

export interface UsageSpend {
  amount?: string;
  currency?: string;
}

export interface UsageQueryResult {
  status?: {
    meters?: {
      meterDefinition?: string;
      meterName?: string;
      spend?: UsageSpend;
      groupings?: { series?: { total?: string }[] }[];
    }[];
    spend?: UsageSpend;
    spendError?: string;
  };
}

export interface BillingAccountBalance {
  status?: {
    currency?: string;
    balance?: { creditRemaining?: string; amountOwed?: string };
    forecast?: { periodStart?: string; periodEnd?: string; estimatedSpend?: string };
  };
}
