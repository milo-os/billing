// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import "testing"

func TestAllowsSnapshotWrite(t *testing.T) {
	h := &offerWebhook{snapshotWriters: DefaultOfferSnapshotWriters()}

	tests := []struct {
		username string
		want     bool
	}{
		{BillingControllerServiceAccount, true},
		{BillingMiloControlUser, true},
		{"system:serviceaccount:billing-system:other", false},
		{"developer@example.com", false},
	}

	for _, tc := range tests {
		t.Run(tc.username, func(t *testing.T) {
			if got := h.allowsSnapshotWrite(tc.username); got != tc.want {
				t.Fatalf("allowsSnapshotWrite(%q) = %v, want %v", tc.username, got, tc.want)
			}
		})
	}
}
