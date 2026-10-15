// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UsageGranularity is the size of the time buckets a UsageQuery returns.
//
// +enum
type UsageGranularity string

const (
	// UsageGranularityHour returns one bucket per hour.
	UsageGranularityHour UsageGranularity = "Hour"

	// UsageGranularityDay returns one bucket per day.
	UsageGranularityDay UsageGranularity = "Day"

	// UsageGranularityMonth returns one bucket per calendar month.
	UsageGranularityMonth UsageGranularity = "Month"
)

// UsageScopeType is the kind of parent a UsageQuery was made in. It comes from
// the request's parent context, never from the query itself.
//
// +enum
type UsageScopeType string

const (
	// UsageScopePlatform is a request made outside any organization or
	// project, which only platform staff are authorized to make. It may
	// read any billing account.
	UsageScopePlatform UsageScopeType = "Platform"

	// UsageScopeOrganization is a request made in an organization. It may
	// read the billing accounts of that organization.
	UsageScopeOrganization UsageScopeType = "Organization"

	// UsageScopeProject is a request made in a project. It may read only the
	// usage of that project, on every billing account of its organization.
	UsageScopeProject UsageScopeType = "Project"
)

// +genclient
// +genclient:nonNamespaced
// +genclient:onlyVerbs=create
// +kubebuilder:object:root=true
// +k8s:openapi-gen=true

// UsageQuery reads metered usage for billing accounts over a time range.
//
// A UsageQuery is never stored: the client creates one, and the server
// returns it with status filled in, the same way a TokenReview or
// SubjectAccessReview works.
//
// What a query may read is decided by where the request is made, not by the
// spec:
//
//   - In an organization (/organizations/<org>/control-plane/...): the
//     organization's billing accounts.
//   - In a project (/projects/<project>/control-plane/...): only that
//     project's usage, on every billing account of its organization, so a
//     project that moved between billing accounts keeps its history.
//   - On the platform (no parent): any billing account. Only platform staff
//     are authorized to do this, and the query must name billing accounts
//     or projects.
//
// The spec can only narrow that scope. A spec that reaches outside it, such
// as another organization's billing account, is rejected as Forbidden rather
// than returning empty results.
//
// Example:
//
//	apiVersion: usage.billing.miloapis.com/v1alpha1
//	kind: UsageQuery
//	spec:
//	  startTime: "2026-10-05T00:00:00Z"
//	  endTime: "2026-10-13T15:00:00Z"
//	  granularity: Day
//	  groupings:
//	    - groupBy: []
//	    - groupBy: [project_name]
//	    - groupBy: [region]
type UsageQuery struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UsageQuerySpec   `json:"spec"`
	Status UsageQueryStatus `json:"status,omitempty"`
}

// UsageQuerySpec selects the usage to read.
type UsageQuerySpec struct {
	// StartTime is the inclusive start of the time range.
	//
	// +required
	StartTime metav1.Time `json:"startTime"`

	// EndTime is the exclusive end of the time range. It must be after
	// StartTime.
	//
	// +required
	EndTime metav1.Time `json:"endTime"`

	// Granularity splits each series into time buckets of this size: Hour,
	// Day or Month. Leave it empty to get only one total per series for the
	// whole time range.
	//
	// +optional
	Granularity UsageGranularity `json:"granularity,omitempty"`

	// TimeZone is the IANA time zone (for example "Europe/Madrid") that day
	// and month buckets are aligned to. Defaults to UTC.
	//
	// +optional
	TimeZone string `json:"timeZone,omitempty"`

	// BillingAccounts narrows the query to these billing accounts. Leave it
	// empty to read every billing account in scope: all of the
	// organization's in an organization, or, on the platform, those of the
	// organizations that own spec.projects. Platform queries must set this
	// or spec.projects. It cannot be set in a project: project members may
	// not be allowed to see their organization's billing accounts.
	//
	// +optional
	// +listType=atomic
	BillingAccounts []BillingAccountReference `json:"billingAccounts,omitempty"`

	// Projects narrows the query to usage reported by these projects (the
	// project_name dimension). In a project, the request's own project is
	// always applied, and naming any other project is rejected.
	//
	// +optional
	// +listType=atomic
	Projects []string `json:"projects,omitempty"`

	// Meters lists the MeterDefinitions to read, by name. Leave it empty to
	// read every published or deprecated MeterDefinition; a grouping by a
	// dimension only some of them have then has no series for the others.
	//
	// +optional
	// +listType=atomic
	Meters []string `json:"meters,omitempty"`

	// Groupings lists the breakdowns to return for every meter. Each
	// grouping produces one set of series, one series per combination of
	// values of its groupBy dimensions. A grouping with an empty groupBy is
	// the meter's total. Leave it empty to get only the total.
	//
	// +optional
	// +listType=atomic
	Groupings []UsageGrouping `json:"groupings,omitempty"`

	// IncludeSpend also returns what each meter's usage costs
	// (status.meters[].spend), priced with each billing account's current
	// plan the same way its invoice is. It costs extra backend queries, so it
	// is off by default.
	//
	// +optional
	IncludeSpend bool `json:"includeSpend,omitempty"`
}

// BillingAccountReference names a billing account.
type BillingAccountReference struct {
	// Namespace is the namespace of the billing account. In an
	// organization it defaults to the organization's namespace, and any
	// other namespace is rejected. Platform queries must set it.
	//
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Name is the name of the billing account.
	//
	// +required
	Name string `json:"name"`
}

// UsageGrouping is one breakdown of a meter's usage.
type UsageGrouping struct {
	// GroupBy lists the dimensions to break usage down by. Each must be
	// declared by the MeterDefinition (spec.measurement.dimensions) or be a
	// system dimension such as project_name. Empty means no breakdown: the
	// meter's total.
	//
	// +optional
	// +listType=atomic
	GroupBy []string `json:"groupBy,omitempty"`
}

// UsageQueryStatus holds the query's results.
type UsageQueryStatus struct {
	// Scope is where the request was made, which decided what the query
	// could read.
	//
	// +optional
	Scope UsageScope `json:"scope,omitempty"`

	// BillingAccounts lists the billing accounts the results cover. It is
	// left empty for a project query, which does not reveal its
	// organization's billing accounts.
	//
	// +optional
	// +listType=atomic
	BillingAccounts []BillingAccountReference `json:"billingAccounts,omitempty"`

	// Meters holds one result per meter read, in the order of spec.meters
	// when it is set.
	//
	// +optional
	// +listType=atomic
	Meters []MeterUsage `json:"meters,omitempty"`

	// Spend is the sum of every meter's spend in Meters, when
	// spec.includeSpend is set: what the usage the query read costs
	// altogether. Meters no plan prices add nothing. Left empty when no
	// meter is priced, or when SpendError is set.
	//
	// +optional
	Spend *UsageSpend `json:"spend,omitempty"`

	// SpendError explains why Spend is left out: some meter's usage or
	// spend could not be read, or the meters are billed in different
	// currencies. The meters' own results are still returned.
	//
	// +optional
	SpendError string `json:"spendError,omitempty"`
}

// UsageScope identifies the parent a UsageQuery was made in.
type UsageScope struct {
	// Type is Platform, Organization or Project.
	//
	// +optional
	Type UsageScopeType `json:"type,omitempty"`

	// Name is the name of the organization or project. Empty for Platform.
	//
	// +optional
	Name string `json:"name,omitempty"`
}

// MeterUsage is the usage of one meter.
type MeterUsage struct {
	// MeterDefinition is the name of the MeterDefinition.
	//
	// +required
	MeterDefinition string `json:"meterDefinition"`

	// MeterName is the MeterDefinition's spec.meterName.
	//
	// +optional
	MeterName string `json:"meterName,omitempty"`

	// Aggregation is how the meter combines events (Sum, Max, Count, ...),
	// from the MeterDefinition. Every value in the results, totals
	// included, uses it.
	//
	// +optional
	Aggregation string `json:"aggregation,omitempty"`

	// Unit is the unit of every value, from the MeterDefinition.
	//
	// +optional
	Unit string `json:"unit,omitempty"`

	// Groupings holds one result per spec.groupings entry, in the same
	// order.
	//
	// +optional
	// +listType=atomic
	Groupings []UsageGroupingResult `json:"groupings,omitempty"`

	// Error explains why this meter has no results, for example because it
	// is not set up in the metering backend yet. The other meters are still
	// returned.
	//
	// +optional
	Error string `json:"error,omitempty"`

	// Spend is what the meter's usage over the time range costs, when
	// spec.includeSpend is set. It is priced with the prices of each billing
	// account's plan in effect at the time, the same way its invoice is, so
	// tiered prices continue from the usage earlier in the billing period.
	// Left empty when no billing account's plan prices this meter.
	//
	// +optional
	Spend *UsageSpend `json:"spend,omitempty"`

	// SpendError explains why Spend could not be computed, for example
	// because the billing accounts are billed in different currencies. The
	// usage results are still returned.
	//
	// +optional
	SpendError string `json:"spendError,omitempty"`
}

// UsageSpend is what usage costs.
type UsageSpend struct {
	// Amount is the cost as a decimal string in Currency (for example
	// "12.5"), before taxes, credits, discounts and minimum commitments. In
	// a project, or with spec.projects set, it prices only that usage, as if
	// it were the billing account's only usage.
	//
	// +required
	Amount string `json:"amount"`

	// Currency is the ISO 4217 code of Amount, for example "USD".
	//
	// +required
	Currency string `json:"currency"`
}

// UsageGroupingResult is one breakdown of a meter's usage.
type UsageGroupingResult struct {
	// GroupBy is the grouping's dimensions, as requested.
	//
	// +optional
	// +listType=atomic
	GroupBy []string `json:"groupBy,omitempty"`

	// Series holds one series per combination of dimension values that has
	// usage. Combinations without usage are left out.
	//
	// +optional
	// +listType=atomic
	Series []UsageSeries `json:"series,omitempty"`
}

// UsageSeries is the usage of one combination of dimension values.
type UsageSeries struct {
	// Group holds the value of each groupBy dimension for this series. A
	// dimension that the events did not set has the value "".
	//
	// +optional
	Group map[string]string `json:"group,omitempty"`

	// Total is the meter's aggregation over the whole time range, as a
	// decimal string (for example "412.5"). For Sum and Count it equals the
	// sum of the points; for other aggregations it does not.
	//
	// +required
	Total string `json:"total"`

	// Points holds the time buckets that have usage, oldest first, when
	// spec.granularity is set. Buckets without usage are left out, not
	// returned as zero.
	//
	// +optional
	// +listType=atomic
	Points []UsagePoint `json:"points,omitempty"`
}

// UsagePoint is the usage in one time bucket.
type UsagePoint struct {
	// Start is the inclusive start of the bucket.
	//
	// +required
	Start metav1.Time `json:"start"`

	// End is the exclusive end of the bucket.
	//
	// +required
	End metav1.Time `json:"end"`

	// Value is the meter's aggregation over the bucket, as a decimal string
	// (for example "18.2").
	//
	// +required
	Value string `json:"value"`
}

// +kubebuilder:object:root=true

// UsageQueryList is a list of UsageQuery objects. UsageQuery cannot be
// listed; the type exists for API machinery.
type UsageQueryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []UsageQuery `json:"items"`
}
