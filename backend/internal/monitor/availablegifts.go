package monitor

import (
	"log"
)

func (m *Monitor) FetchAvailableGifts() ([]string, error) {
	if cached := m.CachedAvailableGifts(); len(cached) > 0 {
		return cached, nil
	}
	m.mu.Lock()
	stdin := m.stdin
	m.mu.Unlock()
	if stdin != nil {
		go m.requestAvailableGifts()
	}
	return []string{}, nil
}

func (m *Monitor) requestAvailableGifts() {
	if err := m.sendBridge(map[string]interface{}{
		"action": "fetch-gifts",
	}); err != nil {
		log.Printf("[Monitor] request available gifts: %v", err)
	}
}

// CachedAvailableGifts returns a copy of the last non-empty gift catalog.
func (m *Monitor) CachedAvailableGifts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.availableGifts) == 0 {
		return nil
	}
	out := make([]string, len(m.availableGifts))
	copy(out, m.availableGifts)
	return out
}

func (m *Monitor) cacheAvailableGifts(names []string) {
	if len(names) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.availableGifts = append([]string(nil), names...)
}

func parseGiftNames(data EventData) []string {
	raw, ok := data["gifts"]
	if !ok || raw == nil {
		return nil
	}
	switch names := raw.(type) {
	case []string:
		out := make([]string, 0, len(names))
		for _, s := range names {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(names))
		for _, n := range names {
			if s, ok := n.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
