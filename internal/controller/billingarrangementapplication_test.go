// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"strings"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

func approvingArrangement(name, application string, created int) *billingv1alpha1.BillingArrangement {
	arr := testArrangement(name, dayPtr(created), nil)
	arr.CreationTimestamp = metav1.NewTime(day(created))
	arr.Spec.ApplicationRef = &billingv1alpha1.BillingArrangementApplicationRef{Name: application}
	return &arr
}

func testDecline(name, application string, created int) *billingv1alpha1.BillingArrangementDecline {
	return &billingv1alpha1.BillingArrangementDecline{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "org-acme", CreationTimestamp: metav1.NewTime(day(created))},
		Spec: billingv1alpha1.BillingArrangementDeclineSpec{
			ApplicationRef: billingv1alpha1.BillingArrangementApplicationRef{Name: application},
			Message:        "Add a card to continue.",
			InternalNotes:  "Internal note that must never appear",
		},
	}
}

func TestBillingArrangementApplicationReconcile(t *testing.T) {
	tests := []struct {
		name          string
		withdrawn     bool
		decisions     []client.Object
		wantPhase     billingv1alpha1.BillingArrangementApplicationPhase
		wantDecided   metav1.ConditionStatus
		wantArr       string
		wantMessage   string
		wantDecidedAt int
	}{
		{
			name:        "awaiting review",
			wantPhase:   billingv1alpha1.BillingArrangementApplicationPhasePending,
			wantDecided: metav1.ConditionFalse,
		},
		{
			name:          "approved",
			decisions:     []client.Object{approvingArrangement("terms", "apply", 6)},
			wantPhase:     billingv1alpha1.BillingArrangementApplicationPhaseApproved,
			wantDecided:   metav1.ConditionTrue,
			wantArr:       "terms",
			wantDecidedAt: 6,
		},
		{
			name:          "declined copies the message but not the notes",
			decisions:     []client.Object{testDecline("no", "apply", 7)},
			wantPhase:     billingv1alpha1.BillingArrangementApplicationPhaseDeclined,
			wantDecided:   metav1.ConditionTrue,
			wantMessage:   "Add a card to continue.",
			wantDecidedAt: 7,
		},
		{
			name:        "withdrawn",
			withdrawn:   true,
			wantPhase:   billingv1alpha1.BillingArrangementApplicationPhaseWithdrawn,
			wantDecided: metav1.ConditionTrue,
		},
		{
			name:          "a decision outranks a withdrawal that raced it",
			withdrawn:     true,
			decisions:     []client.Object{testDecline("no", "apply", 7)},
			wantPhase:     billingv1alpha1.BillingArrangementApplicationPhaseDeclined,
			wantDecided:   metav1.ConditionTrue,
			wantMessage:   "Add a card to continue.",
			wantDecidedAt: 7,
		},
		{
			name: "an approval outranks a decline that raced it",
			decisions: []client.Object{
				testDecline("no", "apply", 6),
				approvingArrangement("terms", "apply", 7),
			},
			wantPhase:     billingv1alpha1.BillingArrangementApplicationPhaseApproved,
			wantDecided:   metav1.ConditionTrue,
			wantArr:       "terms",
			wantDecidedAt: 7,
		},
		{
			name: "decisions for an earlier application with the same name are ignored",
			decisions: []client.Object{
				approvingArrangement("old-terms", "apply", 1),
				testDecline("old-no", "apply", 2),
			},
			wantPhase:   billingv1alpha1.BillingArrangementApplicationPhasePending,
			wantDecided: metav1.ConditionFalse,
		},
		{
			name: "decisions for other applications are ignored",
			decisions: []client.Object{
				approvingArrangement("terms", "someone-else", 6),
				testDecline("no", "someone-else", 7),
			},
			wantPhase:   billingv1alpha1.BillingArrangementApplicationPhasePending,
			wantDecided: metav1.ConditionFalse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := billingv1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			app := testApplication("apply", "")
			app.CreationTimestamp = metav1.NewTime(day(5))
			app.Spec.Withdrawn = tt.withdrawn

			c := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&billingv1alpha1.BillingArrangementApplication{}).
				WithIndex(&billingv1alpha1.BillingArrangement{}, ArrangementApplicationRefField, arrangementApplicationRef).
				WithIndex(&billingv1alpha1.BillingArrangementDecline{}, DeclineApplicationRefField,
					func(obj client.Object) []string {
						return []string{obj.(*billingv1alpha1.BillingArrangementDecline).Spec.ApplicationRef.Name}
					}).
				WithObjects(append(tt.decisions, &app)...).Build()

			r := &BillingArrangementApplicationReconciler{client: c}
			key := types.NamespacedName{Name: "apply", Namespace: "org-acme"}
			if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}

			var got billingv1alpha1.BillingArrangementApplication
			if err := c.Get(context.Background(), key, &got); err != nil {
				t.Fatal(err)
			}
			if got.Status.Phase != tt.wantPhase {
				t.Errorf("phase = %s, want %s", got.Status.Phase, tt.wantPhase)
			}
			cond := apimeta.FindStatusCondition(got.Status.Conditions, billingv1alpha1.BillingArrangementApplicationConditionDecided)
			if cond == nil || cond.Status != tt.wantDecided || cond.Reason != string(tt.wantPhase) {
				t.Errorf("Decided condition = %+v, want %s/%s", cond, tt.wantDecided, tt.wantPhase)
			}
			gotArr := ""
			if got.Status.ArrangementRef != nil {
				gotArr = got.Status.ArrangementRef.Name
			}
			if gotArr != tt.wantArr {
				t.Errorf("arrangementRef = %q, want %q", gotArr, tt.wantArr)
			}
			if got.Status.DeclineMessage != tt.wantMessage {
				t.Errorf("declineMessage = %q, want %q", got.Status.DeclineMessage, tt.wantMessage)
			}
			if strings.Contains(cond.Message, "Internal") || strings.Contains(got.Status.DeclineMessage, "Internal") {
				t.Errorf("internal notes leaked into status: %+v", got.Status)
			}
			switch {
			case tt.wantDecidedAt == 0 && got.Status.DecidedAt != nil:
				t.Errorf("decidedAt = %s, want unset", got.Status.DecidedAt)
			case tt.wantDecidedAt != 0 && (got.Status.DecidedAt == nil || !got.Status.DecidedAt.Time.Equal(day(tt.wantDecidedAt))):
				t.Errorf("decidedAt = %v, want %s", got.Status.DecidedAt, day(tt.wantDecidedAt))
			}
		})
	}
}
