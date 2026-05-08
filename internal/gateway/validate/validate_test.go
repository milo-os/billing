// SPDX-License-Identifier: AGPL-3.0-only

package validate_test

import (
	"encoding/json"
	"testing"

	"go.miloapis.com/billing/internal/gateway/validate"
)

// validEvent returns a JSON-encoded CloudEvent that passes all structural checks.
func validEvent(t *testing.T, overrides map[string]any) json.RawMessage {
	t.Helper()
	base := map[string]any{
		"specversion":     "1.0",
		"type":            "com.example.usage",
		"source":          "/services/example",
		"subject":         "projects/p-abc123",
		"id":              "01HQ3YYZV3FDZM0B1NV1KPGWEP",
		"datacontenttype": "application/json",
		"data":            map[string]any{"value": "42"},
	}
	for k, v := range overrides {
		if v == nil {
			delete(base, k)
		} else {
			base[k] = v
		}
	}
	b, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshaling test event: %v", err)
	}
	return b
}

func TestValidateEvent_valid(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, nil))
	if !result.Valid {
		t.Fatalf("expected valid, got reason=%s detail=%s", result.Reason, result.Detail)
	}
	if result.ID != "01HQ3YYZV3FDZM0B1NV1KPGWEP" {
		t.Errorf("unexpected ID: %s", result.ID)
	}
}

func TestValidateEvent_invalidULID_absent(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"id": nil}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonInvalidULID {
		t.Errorf("expected %s, got %s", validate.ReasonInvalidULID, result.Reason)
	}
}

func TestValidateEvent_invalidULID_notULID(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"id": "not-a-ulid"}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonInvalidULID {
		t.Errorf("expected %s, got %s", validate.ReasonInvalidULID, result.Reason)
	}
}

func TestValidateEvent_missingSpecVersion(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"specversion": nil}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonMissingRequiredField {
		t.Errorf("expected %s, got %s", validate.ReasonMissingRequiredField, result.Reason)
	}
}

func TestValidateEvent_missingType(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"type": nil}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonMissingRequiredField {
		t.Errorf("expected %s, got %s", validate.ReasonMissingRequiredField, result.Reason)
	}
}

func TestValidateEvent_missingSource(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"source": nil}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonMissingRequiredField {
		t.Errorf("expected %s, got %s", validate.ReasonMissingRequiredField, result.Reason)
	}
}

func TestValidateEvent_missingSubject(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"subject": nil}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonMissingRequiredField {
		t.Errorf("expected %s, got %s", validate.ReasonMissingRequiredField, result.Reason)
	}
}

func TestValidateEvent_subjectBadFormat(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"subject": "orgs/o-abc"}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonMissingRequiredField {
		t.Errorf("expected %s, got %s", validate.ReasonMissingRequiredField, result.Reason)
	}
}

func TestValidateEvent_invalidDataContentType(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"datacontenttype": "text/plain"}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonInvalidDataContentType {
		t.Errorf("expected %s, got %s", validate.ReasonInvalidDataContentType, result.Reason)
	}
}

func TestValidateEvent_missingDataContentType(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"datacontenttype": nil}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonInvalidDataContentType {
		t.Errorf("expected %s, got %s", validate.ReasonInvalidDataContentType, result.Reason)
	}
}

func TestValidateEvent_missingDataValue(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"data": map[string]any{}}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonInvalidValueType {
		t.Errorf("expected %s, got %s", validate.ReasonInvalidValueType, result.Reason)
	}
}

func TestValidateEvent_invalidDataValue(t *testing.T) {
	result := validate.ValidateEvent(validEvent(t, map[string]any{"data": map[string]any{"value": "not-a-number"}}))
	if result.Valid {
		t.Fatal("expected invalid")
	}
	if result.Reason != validate.ReasonInvalidValueType {
		t.Errorf("expected %s, got %s", validate.ReasonInvalidValueType, result.Reason)
	}
}

func TestValidateEvent_negativeValue(t *testing.T) {
	// Negative INT64 values are structurally valid.
	result := validate.ValidateEvent(validEvent(t, map[string]any{"data": map[string]any{"value": "-1"}}))
	if !result.Valid {
		t.Fatalf("expected valid for negative value, got reason=%s", result.Reason)
	}
}
