// SPDX-License-Identifier: AGPL-3.0-only

// Package event defines the CloudEvent wire types shared between the emission
// SDK and the billing controllers consumer. Keeping a single definition here
// means any change to the on-wire data payload is a compile error in both the
// producer (emission) and the consumer.
package event

// EventData is the JSON payload of a billing CloudEvent's data field.
// Value is INT64, string-encoded in JSON to avoid precision loss.
type EventData struct {
	Value      int64             `json:"value,string"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
	Resource   *ResourceRef      `json:"resource,omitempty"`
}

// ResourceRef identifies the Kubernetes object that generated the usage event.
type ResourceRef struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}
