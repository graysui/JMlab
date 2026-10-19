package module

import "context"

var eventTopics = map[string]bool{
	"media.item.created": true, "media.item.updated": true, "user.state.updated": true,
	"playback.started": true, "playback.stopped": true, "module.status.changed": true,
}

func (c *HTTPModuleClient) DeliverEvent(ctx context.Context, event Event) error {
	// Notification failures have their own retry/counters and must not disable
	// a working search or playback capability.
	return c.requestJSON(ctx, "POST", "/api/v1/events", event, nil)
}

// Called under the manager lock after publication. Replacement cancels this
// module's old queue and HTTP delivery, then subscribes the new instance.
func (m *Manager) syncEvents(id string) {
	if cancel := m.eventSubscriptions[id]; cancel != nil {
		cancel()
		delete(m.eventSubscriptions, id)
	}
	entry, ok := m.registry.Get(id)
	if !ok || entry.Status != StatusActive || len(entry.Manifest.Subscriptions) == 0 {
		return
	}
	client, _ := entry.MetadataInstance.(*HTTPModuleClient)
	if client == nil {
		client, _ = entry.StreamInstance.(*HTTPModuleClient)
	}
	if client == nil {
		return
	}
	revision := entry.Revision
	m.eventSubscriptions[id] = m.eventBus.SubscribeNamed("module:"+id, entry.Manifest.Subscriptions, func(ctx context.Context, event Event) error {
		current, ok := m.registry.Get(id)
		if !ok || current.Status != StatusActive || current.Revision != revision {
			return nil
		}
		return client.DeliverEvent(ctx, event)
	})
}
func (m *Manager) emitStatus(id, status string) {
	m.eventBus.Emit(context.Background(), "module.status.changed", "", map[string]string{"moduleId": id, "status": status})
}
