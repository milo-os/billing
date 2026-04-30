// SPDX-License-Identifier: AGPL-3.0-only

package emission

import (
	"fmt"
	"net/url"
	"regexp"
)

var projectNameRe = regexp.MustCompile(`^projects/[^/]+$`)

// ValidationError describes a structural validation failure in a UsageEvent.
// It is returned by Record() when the event fails synchronous validation
// before any write to Vector.
type ValidationError struct {
	// Field is the name of the offending UsageEvent field (e.g. "Meter").
	Field string
	// Message describes the violation (e.g. "must not be empty").
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("emission: validation error on field %s: %s", e.Field, e.Message)
}

// validate checks all structural requirements for a UsageEvent.
// It returns the first error found. Callers that need all errors
// can extend this function; v0 stops at the first violation.
func validate(ev UsageEvent) error {
	if ev.Meter == "" {
		return &ValidationError{Field: "Meter", Message: "must not be empty"}
	}

	if ev.Project.Name == "" {
		return &ValidationError{Field: "Project.Name", Message: "must not be empty"}
	}

	if !projectNameRe.MatchString(ev.Project.Name) {
		return &ValidationError{Field: "Project.Name", Message: "must match projects/{id} format"}
	}

	if ev.Source == "" {
		return &ValidationError{Field: "Source", Message: "must not be empty"}
	}

	// Accept both scheme-prefixed URIs (https://...) and protocol-relative
	// resource names (//service.example.com/...) used by GCP-style APIs.
	u, err := url.Parse(ev.Source)
	if err != nil || (u.Scheme == "" && u.Host == "") {
		return &ValidationError{Field: "Source", Message: "must be a valid absolute URI"}
	}

	if ev.Quantity <= 0 {
		return &ValidationError{Field: "Quantity", Message: "must be greater than zero"}
	}

	return nil
}
