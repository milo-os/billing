// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type clusterAwareWebhookServer struct {
	webhook.Server
}

var _ webhook.Server = &clusterAwareWebhookServer{}

func (s *clusterAwareWebhookServer) Register(path string, hook http.Handler) {
	if h, ok := hook.(*admission.Webhook); ok {
		orig := h.Handler
		h.Handler = admission.HandlerFunc(func(ctx context.Context, req admission.Request) admission.Response {
			c := clusterFromExtra(req.UserInfo.Extra)
			ctx = WithClusterName(ctx, c)
			return orig.Handle(ctx, req)
		})
	}

	s.Server.Register(path, hook)
}

// NewClusterAwareWebhookServer wraps a webhook.Server to inject cluster context
// from the admission request's UserInfo extras.
func NewClusterAwareWebhookServer(server webhook.Server) webhook.Server {
	return &clusterAwareWebhookServer{
		Server: server,
	}
}
