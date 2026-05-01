// SPDX-License-Identifier: AGPL-3.0-only

package emission

import (
	"strconv"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

type eventData struct {
	Value      string            `json:"value"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
	Resource   *resourceRefData  `json:"resource,omitempty"`
}

type resourceRefData struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

// toCloudEvent converts a validated UsageEvent into a CloudEvents v1.0 event.
// now is the timestamp to use when ev.OccurredAt is zero.
// id is a pre-generated ULID string.
func toCloudEvent(ev UsageEvent, now time.Time, id string) (cloudevents.Event, error) {
	ce := cloudevents.NewEvent()
	ce.SetID(id)
	ce.SetType(ev.Meter)
	ce.SetSource(ev.Source)
	ce.SetSubject(ev.Project.Name)

	ts := ev.OccurredAt
	if ts.IsZero() {
		ts = now
	}
	ce.SetTime(ts)

	data := eventData{
		Value: strconv.FormatInt(ev.Quantity, 10),
	}

	if len(ev.Dimensions) > 0 {
		data.Dimensions = ev.Dimensions
	}

	if ev.Resource != nil {
		data.Resource = &resourceRefData{
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
