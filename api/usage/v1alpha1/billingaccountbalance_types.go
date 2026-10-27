// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:onlyVerbs=get,list
// +kubebuilder:object:root=true
// +k8s:openapi-gen=true

// BillingAccountBalance reports the running balance and current period forecast
// for a BillingAccount.
//
// A BillingAccountBalance is a read-only, non-persisted virtual resource served
// live by the usage aggregated API server (usage.billing.miloapis.com/v1alpha1).
// It is read via GET or LIST in the billing account's namespace, using the
// billing account name as the resource name.
type BillingAccountBalance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status BillingAccountBalanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:openapi-gen=true

// BillingAccountBalanceList contains a list of BillingAccountBalance.
type BillingAccountBalanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BillingAccountBalance `json:"items"`
}

// BillingAccountBalanceStatus carries the observed balance, forecast and active grants.
type BillingAccountBalanceStatus struct {
	// RetrievedAt is the timestamp when the balance was computed by the provider.
	//
	// +required
	RetrievedAt metav1.Time `json:"retrievedAt"`

	// Currency is the ISO 4217 currency code for all amounts (USD).
	//
	// +required
	Currency string `json:"currency"`

	// Balance represents the posted ledger state (finalized invoices and grants).
	//
	// +required
	Balance AccountBalanceSummary `json:"balance"`

	// Forecast represents un-invoiced, forward-looking activity for the current period.
	//
	// +required
	Forecast AccountBalanceForecast `json:"forecast"`

	// Grants lists individual active credit grants, ordered soonest-expiring first.
	//
	// +optional
	Grants []BillingAccountBalanceGrant `json:"grants,omitempty"`
}

// AccountBalanceSummary represents the posted ledger balance.
type AccountBalanceSummary struct {
	// CreditRemaining is the total posted credit balance remaining across all grants,
	// expressed as a decimal string (e.g. "130.00").
	//
	// +required
	CreditRemaining string `json:"creditRemaining"`

	// AmountOwed is the total unpaid/outstanding amount from finalized past invoices,
	// expressed as a decimal string (e.g. "75.00").
	//
	// +required
	AmountOwed string `json:"amountOwed"`
}

// AccountBalanceForecast represents forward-looking un-invoiced activity for the current period.
type AccountBalanceForecast struct {
	// PeriodStart is the start of the current billing period.
	//
	// +required
	PeriodStart metav1.Time `json:"periodStart"`

	// PeriodEnd is the end of the current billing period.
	//
	// +required
	PeriodEnd metav1.Time `json:"periodEnd"`

	// EstimatedSpend is the metered usage charges accumulated so far this period,
	// priced as the invoice will price it, expressed as a decimal string (e.g. "60.00").
	//
	// +required
	EstimatedSpend string `json:"estimatedSpend"`

	// EstimatedCreditUsed is the amount of available credit projected to offset current period spend,
	// expressed as a decimal string (e.g. "60.00").
	//
	// +required
	EstimatedCreditUsed string `json:"estimatedCreditUsed"`

	// EstimatedAmountDue is the projected amount that will be due if invoiced now
	// (estimatedSpend - estimatedCreditUsed), expressed as a decimal string (e.g. "0.00").
	//
	// +required
	EstimatedAmountDue string `json:"estimatedAmountDue"`

	// EstimatedCreditRemaining is the projected credit remaining after covering current period spend,
	// expressed as a decimal string (e.g. "70.00").
	//
	// +required
	EstimatedCreditRemaining string `json:"estimatedCreditRemaining"`
}

// BillingAccountBalanceGrant describes one active credit grant.
type BillingAccountBalanceGrant struct {
	// Name is the name of the CreditGrant resource.
	//
	// +required
	Name string `json:"name"`

	// Amount is the initial total granted amount, expressed as a decimal string.
	//
	// +required
	Amount string `json:"amount"`

	// Used is the amount of this grant consumed by past invoices, expressed as a decimal string.
	//
	// +required
	Used string `json:"used"`

	// Remaining is the unconsumed amount of this grant, expressed as a decimal string.
	//
	// +required
	Remaining string `json:"remaining"`

	// ExpiresAt is the optional expiration timestamp for this grant. Omitted if the grant never expires.
	//
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}
