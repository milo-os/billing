// SPDX-License-Identifier: AGPL-3.0-only

package emission

import (
	cloudevents "github.com/cloudevents/sdk-go/v2"

	"go.miloapis.com/billing/internal/event"
)

// toCloudEvent converts a validated UsageEvent into a CloudEvents v1.0 event.
// id is a pre-generated ULID string.
func toCloudEvent(ev UsageEvent, id string) (cloudevents.Event, error) {
	ce := cloudevents.NewEvent()
	ce.SetID(id)
	ce.SetType(ev.Meter)
	ce.SetSource(ev.Source)
	ce.SetSubject("projects/" + ev.Project.Name)
	ce.SetTime(ev.OccurredAt)

	data := event.EventData{
		Value: ev.Quantity,
	}

	if len(ev.Dimensions) > 0 {
		data.Dimensions = ev.Dimensions
	}

	if ev.Resource != nil {
		data.Resource = &event.ResourceRef{
			Group:     ev.Resource.Group,
			Kind:      ev.Resource.Kind,
			Namespace: ev.Resource.Namespace,
			Name:      ev.Resource.Name,
			UID:       string(ev.Resource.UID),
		}
	}

	if err := ce.SetData("application/json", data); err != nil {
		return cloudevents.Event{}, err
	}

	return ce, nil
}
