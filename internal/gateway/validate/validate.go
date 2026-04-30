// SPDX-License-Identifier: AGPL-3.0-only

// Package validate provides structural validation of CloudEvents for the
// ingestion gateway. Business rule validation (meter existence, dimension
// conformance) is explicitly out of scope and owned by the billing controllers.
package validate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oklog/ulid/v2"
)

// RejectionReason is a SCREAMING_SNAKE_CASE rejection reason string.
// All values are fixed constants to control metric label cardinality.
type RejectionReason string

const (
	// ReasonInvalidULID is returned when the CloudEvent id field cannot be
	// parsed as a valid ULID.
	ReasonInvalidULID RejectionReason = "INVALID_ULID"

	// ReasonMissingRequiredField is returned when a required CloudEvents
	// attribute is absent, or when the subject does not match projects/{id}.
	ReasonMissingRequiredField RejectionReason = "MISSING_REQUIRED_FIELD"

	// ReasonInvalidValueType is returned when data.value is absent or cannot
	// be parsed as a valid INT64 string.
	ReasonInvalidValueType RejectionReason = "INVALID_VALUE_TYPE"

	// ReasonInvalidDataContentType is returned when datacontenttype is not
	// "application/json".
	ReasonInvalidDataContentType RejectionReason = "INVALID_DATACONTENTTYPE"
)

// EventResult holds the outcome of validating a single CloudEvent.
type EventResult struct {
	// ID is the CloudEvent id field (empty string if id is absent/malformed).
	ID string
	// Reason is set when the event is rejected.
	Reason RejectionReason
	// Detail is a human-readable description of the failure.
	Detail string
	// Valid is true when the event passed all structural checks.
	Valid bool
}

// cloudEvent is an internal struct for partial decoding of a CloudEvent JSON
// payload. All fields are pointers so we can distinguish absent from zero.
type cloudEvent struct {
	ID              *string          `json:"id"`
	SpecVersion     *string          `json:"specversion"`
	Type            *string          `json:"type"`
	Source          *string          `json:"source"`
	Subject         *string          `json:"subject"`
	DataContentType *string          `json:"datacontenttype"`
	Data            *json.RawMessage `json:"data"`
}

// eventData is an internal struct for decoding the data field of a CloudEvent.
type eventData struct {
	Value *string `json:"value"`
}

// ValidateEvent validates a single CloudEvent for structural correctness.
// It does not perform business rule validation (meter existence, etc.).
// Validation stops at the first failure; the returned EventResult always has
// Valid set. The function accepts json.RawMessage so it can validate
// partially-malformed inputs without panicking on missing fields.
func ValidateEvent(raw json.RawMessage) EventResult {
	var ce cloudEvent
	if err := json.Unmarshal(raw, &ce); err != nil {
		return EventResult{
			Reason: ReasonMissingRequiredField,
			Detail: "event is not valid JSON",
		}
	}

	// 1. id must be present and parseable as a ULID.
	if ce.ID == nil || *ce.ID == "" {
		return EventResult{
			Reason: ReasonInvalidULID,
			Detail: "required attribute 'id' is absent",
		}
	}
	if _, err := ulid.Parse(*ce.ID); err != nil {
		return EventResult{
			ID:     *ce.ID,
			Reason: ReasonInvalidULID,
			Detail: fmt.Sprintf("id %q is not a valid ULID: %v", *ce.ID, err),
		}
	}

	// 2. Required CloudEvents attributes.
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"specversion", ce.SpecVersion},
		{"type", ce.Type},
		{"source", ce.Source},
		{"subject", ce.Subject},
	} {
		if field.value == nil || *field.value == "" {
			return EventResult{
				ID:     *ce.ID,
				Reason: ReasonMissingRequiredField,
				Detail: fmt.Sprintf("required attribute '%s' is absent", field.name),
			}
		}
	}

	// 3. subject must match projects/{id}.
	if !strings.HasPrefix(*ce.Subject, "projects/") || len(*ce.Subject) <= len("projects/") {
		return EventResult{
			ID:     *ce.ID,
			Reason: ReasonMissingRequiredField,
			Detail: fmt.Sprintf("subject %q does not match required format projects/{id}", *ce.Subject),
		}
	}

	// 4. datacontenttype must be "application/json".
	if ce.DataContentType == nil || *ce.DataContentType != "application/json" {
		got := "<absent>"
		if ce.DataContentType != nil {
			got = *ce.DataContentType
		}
		return EventResult{
			ID:     *ce.ID,
			Reason: ReasonInvalidDataContentType,
			Detail: fmt.Sprintf("datacontenttype must be 'application/json', got %q", got),
		}
	}

	// 5. data.value must be present and parseable as INT64.
	if ce.Data == nil {
		return EventResult{
			ID:     *ce.ID,
			Reason: ReasonInvalidValueType,
			Detail: "data field is absent",
		}
	}
	var data eventData
	if err := json.Unmarshal(*ce.Data, &data); err != nil {
		return EventResult{
			ID:     *ce.ID,
			Reason: ReasonInvalidValueType,
			Detail: fmt.Sprintf("data field is not valid JSON: %v", err),
		}
	}
	if data.Value == nil || *data.Value == "" {
		return EventResult{
			ID:     *ce.ID,
			Reason: ReasonInvalidValueType,
			Detail: "data.value is absent",
		}
	}
	// Validate INT64 string: must be parseable as a signed 64-bit integer.
	var parsed int64
	if _, err := fmt.Sscanf(*data.Value, "%d", &parsed); err != nil {
		return EventResult{
			ID:     *ce.ID,
			Reason: ReasonInvalidValueType,
			Detail: fmt.Sprintf("data.value %q is not a valid INT64 string: %v", *data.Value, err),
		}
	}

	return EventResult{
		ID:    *ce.ID,
		Valid: true,
	}
}
