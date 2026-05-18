// SPDX-License-Identifier: AGPL-3.0-only

package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	stripego "github.com/stripe/stripe-go/v81"
	stripewebhook "github.com/stripe/stripe-go/v81/webhook"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
)

const (
	// WebhookPath is the public HTTP route Stripe is configured to send
	// events to.
	WebhookPath = "/v1alpha1/webhooks/stripe"

	// maxWebhookBodyBytes caps the request body we read. Stripe events
	// are well under 256KiB in practice.
	maxWebhookBodyBytes = 1 << 18

	// webhookSignatureTolerance is the allowed clock skew between Stripe
	// and the receiver when verifying the t= timestamp on the signature
	// header.
	webhookSignatureTolerance = 5 * time.Minute
)

// WebhookHandler serves the Stripe webhook endpoint. It resolves the
// PaymentProvider's webhook secret per request (so rotating the secret
// requires no restart), verifies the signature, dedupes on event id, and
// applies side effects to BillingAccount / PaymentMethodSetup status.
type WebhookHandler struct {
	// Client is the controller-runtime client used to read PaymentProvider
	// and to patch BillingAccount / PaymentMethodSetup status.
	Client client.Client

	// ProviderName is the name of the PaymentProvider this handler
	// authenticates against. There is one handler per provider; in the
	// common single-provider deployment this is just the stripe provider.
	ProviderName string

	// SeenEvents records Stripe event ids we've already processed, for
	// idempotency. Nil disables deduping (acceptable for tests).
	SeenEvents EventDeduper
}

// EventDeduper records and checks Stripe event ids for at-most-once
// processing semantics. Production implementations should persist across
// restarts (e.g. a CRD or a CR annotation); for the initial implementation
// an in-memory ring is acceptable since Stripe retries automatically and
// the webhook handler is idempotent on the underlying CR patches.
type EventDeduper interface {
	// SeenOrRecord returns true if the event id was already recorded.
	// Otherwise it records the id and returns false.
	SeenOrRecord(eventID string) bool
}

var webhookLog = ctrl.Log.WithName("stripe-webhook")

// ServeHTTP implements http.Handler.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
	if err != nil {
		webhookLog.Error(err, "reading webhook body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sig := r.Header.Get("Stripe-Signature")
	if sig == "" {
		http.Error(w, "missing Stripe-Signature", http.StatusBadRequest)
		return
	}

	cfg, err := ResolveProviderConfig(ctx, h.Client, h.ProviderName)
	if err != nil {
		webhookLog.Error(err, "resolving provider config for webhook", "provider", h.ProviderName)
		// 500 — Stripe will retry. Returning 4xx here would mark the
		// event as permanently delivered which would mask an outage.
		http.Error(w, "provider unavailable", http.StatusInternalServerError)
		return
	}

	event, err := stripewebhook.ConstructEventWithOptions(body, sig, cfg.WebhookSecret, stripewebhook.ConstructEventOptions{
		Tolerance: webhookSignatureTolerance,
	})
	if err != nil {
		webhookLog.Info("rejecting webhook with invalid signature", "err", err.Error())
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	if h.SeenEvents != nil && h.SeenEvents.SeenOrRecord(event.ID) {
		webhookLog.V(1).Info("dropping duplicate event", "id", event.ID, "type", event.Type)
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.dispatch(ctx, &event); err != nil {
		webhookLog.Error(err, "dispatching event", "id", event.ID, "type", event.Type)
		// Surface as 500 so Stripe retries. The dedupe layer ensures
		// retries are safe.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) dispatch(ctx context.Context, event *stripego.Event) error {
	switch event.Type {
	case "setup_intent.succeeded":
		return h.handleSetupIntentSucceeded(ctx, event)
	case "setup_intent.setup_failed", "setup_intent.canceled":
		return h.handleSetupIntentFailed(ctx, event)
	default:
		webhookLog.V(1).Info("ignoring unhandled event type", "type", event.Type, "id", event.ID)
		return nil
	}
}

// setupIntentEvent extracts the SetupIntent payload from a webhook event.
type setupIntentEvent struct {
	ID                  string `json:"id"`
	Status              string `json:"status"`
	Customer            string `json:"customer"`
	PaymentMethod       string `json:"payment_method"`
	LastSetupError      *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"last_setup_error"`
	Metadata map[string]string `json:"metadata"`
}

func decodeSetupIntent(event *stripego.Event) (*setupIntentEvent, error) {
	if event.Data == nil || len(event.Data.Raw) == 0 {
		return nil, errors.New("event has no data")
	}
	var si setupIntentEvent
	if err := json.Unmarshal(event.Data.Raw, &si); err != nil {
		return nil, fmt.Errorf("decoding SetupIntent payload: %w", err)
	}
	if si.ID == "" {
		return nil, errors.New("SetupIntent payload has no id")
	}
	return &si, nil
}

func (h *WebhookHandler) handleSetupIntentSucceeded(ctx context.Context, event *stripego.Event) error {
	si, err := decodeSetupIntent(event)
	if err != nil {
		return err
	}

	setup, err := h.findSetupForSetupIntent(ctx, si)
	if err != nil {
		return err
	}
	if setup == nil {
		webhookLog.Info("no PaymentMethodSetup found for SetupIntent; skipping", "setupIntent", si.ID)
		return nil
	}

	// Resolve a client for downstream API calls (PaymentMethod fetch).
	cfg, err := ResolveProviderConfig(ctx, h.Client, h.ProviderName)
	if err != nil {
		return err
	}
	stripe := NewClient(cfg)
	pm, err := stripe.RetrievePaymentMethod(ctx, si.PaymentMethod)
	if err != nil {
		return fmt.Errorf("retrieving PaymentMethod for SetupIntent %q: %w", si.ID, err)
	}

	if err := h.patchBillingAccountPaymentMethod(ctx, setup, si, pm); err != nil {
		return err
	}
	return h.patchSetupSucceeded(ctx, setup, si)
}

func (h *WebhookHandler) handleSetupIntentFailed(ctx context.Context, event *stripego.Event) error {
	si, err := decodeSetupIntent(event)
	if err != nil {
		return err
	}
	setup, err := h.findSetupForSetupIntent(ctx, si)
	if err != nil {
		return err
	}
	if setup == nil {
		return nil
	}
	return h.patchSetupFailed(ctx, setup, si)
}

// findSetupForSetupIntent locates the PaymentMethodSetup CR corresponding
// to a Stripe SetupIntent. Preference order:
//  1. metadata["payment_method_setup"] set at creation time — fast path.
//  2. fallback: list PaymentMethodSetups and match on status.setupIntentId.
func (h *WebhookHandler) findSetupForSetupIntent(ctx context.Context, si *setupIntentEvent) (*billingv1alpha1.PaymentMethodSetup, error) {
	if name := si.Metadata["payment_method_setup"]; name != "" {
		// Metadata format is "namespace/name". The reconciler always
		// sets it that way when creating the SetupIntent.
		ns, n := splitNamespacedName(name)
		var s billingv1alpha1.PaymentMethodSetup
		if err := h.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: n}, &s); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("getting PaymentMethodSetup %s/%s: %w", ns, n, err)
		}
		return &s, nil
	}
	var list billingv1alpha1.PaymentMethodSetupList
	if err := h.Client.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("listing PaymentMethodSetups: %w", err)
	}
	for i := range list.Items {
		if list.Items[i].Status.SetupIntentID == si.ID {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

func splitNamespacedName(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i], s[i+1:]
		}
	}
	return "", s
}

func (h *WebhookHandler) patchBillingAccountPaymentMethod(ctx context.Context, setup *billingv1alpha1.PaymentMethodSetup, si *setupIntentEvent, pm *PaymentMethodDetails) error {
	var ba billingv1alpha1.BillingAccount
	key := types.NamespacedName{Namespace: setup.Namespace, Name: setup.Spec.BillingAccountRef.Name}
	if err := h.Client.Get(ctx, key, &ba); err != nil {
		return fmt.Errorf("getting BillingAccount %s/%s: %w", key.Namespace, key.Name, err)
	}
	now := metav1.Now()
	patch := client.MergeFrom(ba.DeepCopy())
	ba.Status.PaymentMethod = &billingv1alpha1.PaymentMethodInfo{
		ProviderCustomerID: si.Customer,
		PaymentMethodID:    pm.ID,
		SetupIntentID:      si.ID,
		Brand:              pm.Brand,
		Last4:              pm.Last4,
		BIN:                pm.BIN,
		Country:            pm.Country,
		ExpMonth:           pm.ExpMonth,
		ExpYear:            pm.ExpYear,
		AVSResult:          pm.AVSResult,
		CVCResult:          pm.CVCResult,
		AttachedAt:         &now,
	}
	setCondition(&ba.Status.Conditions, billingv1alpha1.BillingAccountConditionPaymentMethodAttached,
		metav1.ConditionTrue, "PaymentMethodAttached",
		fmt.Sprintf("Stripe SetupIntent %s succeeded; payment method %s attached.", si.ID, pm.ID),
		ba.Generation)
	return h.Client.Status().Patch(ctx, &ba, patch)
}

func (h *WebhookHandler) patchSetupSucceeded(ctx context.Context, setup *billingv1alpha1.PaymentMethodSetup, si *setupIntentEvent) error {
	patch := client.MergeFrom(setup.DeepCopy())
	setup.Status.Phase = billingv1alpha1.PaymentMethodSetupPhaseSucceeded
	setup.Status.SetupIntentStatus = si.Status
	setCondition(&setup.Status.Conditions, billingv1alpha1.PaymentMethodSetupConditionSucceeded,
		metav1.ConditionTrue, "SetupIntentSucceeded",
		fmt.Sprintf("Stripe SetupIntent %s succeeded.", si.ID),
		setup.Generation)
	return h.Client.Status().Patch(ctx, setup, patch)
}

func (h *WebhookHandler) patchSetupFailed(ctx context.Context, setup *billingv1alpha1.PaymentMethodSetup, si *setupIntentEvent) error {
	patch := client.MergeFrom(setup.DeepCopy())
	setup.Status.Phase = billingv1alpha1.PaymentMethodSetupPhaseFailed
	setup.Status.SetupIntentStatus = si.Status
	if si.LastSetupError != nil {
		setup.Status.FailureReason = si.LastSetupError.Code
		setup.Status.FailureMessage = si.LastSetupError.Message
	}
	reason := "SetupIntentFailed"
	msg := fmt.Sprintf("Stripe SetupIntent %s failed.", si.ID)
	if si.LastSetupError != nil && si.LastSetupError.Message != "" {
		msg = si.LastSetupError.Message
	}
	setCondition(&setup.Status.Conditions, billingv1alpha1.PaymentMethodSetupConditionSucceeded,
		metav1.ConditionFalse, reason, msg, setup.Generation)
	return h.Client.Status().Patch(ctx, setup, patch)
}

// setCondition upserts a condition by type, preserving LastTransitionTime
// only when the status is unchanged.
func setCondition(conditions *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, message string, observedGen int64) {
	now := metav1.Now()
	for i := range *conditions {
		if (*conditions)[i].Type == condType {
			if (*conditions)[i].Status != status {
				(*conditions)[i].LastTransitionTime = now
			}
			(*conditions)[i].Status = status
			(*conditions)[i].Reason = reason
			(*conditions)[i].Message = message
			(*conditions)[i].ObservedGeneration = observedGen
			return
		}
	}
	*conditions = append(*conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: observedGen,
	})
}
