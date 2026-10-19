package userdata

import (
	"context"
	"fmt"
	"github.com/graysui/JMlab/internal/module"
)

func (s *Service) SetEventBus(events *module.EventBus) { s.events = events }
func (s *Service) emitState(ctx context.Context, topic, userID, itemID string, version int) {
	if s.events == nil {
		return
	}
	s.events.Emit(ctx, topic, fmt.Sprintf("state:%s:%s:%d", userID, itemID, version), map[string]any{"userId": userID, "itemId": itemID, "version": version})
}
