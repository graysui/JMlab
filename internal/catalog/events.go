package catalog

import (
	"context"
	"github.com/graysui/JMlab/internal/module"
)

// Configure before serving requests. Events contain stable IDs only, never
// source credentials, signed media addresses, or personal notes.
func (s *Service) SetEventBus(events *module.EventBus) {
	s.events = events
	if events != nil {
		events.SubscribeNamed("catalog:library-memberships", []string{"module.status.changed"}, func(ctx context.Context, _ module.Event) error {
			return s.SyncLibraryMemberships(ctx, "", "")
		})
	}
}
func (s *Service) emitMedia(ctx context.Context, itemID string, created bool) {
	if s.events == nil {
		return
	}
	topic := "media.item.updated"
	if created {
		topic = "media.item.created"
	}
	s.events.Emit(ctx, topic, "", map[string]string{"itemId": itemID})
}
