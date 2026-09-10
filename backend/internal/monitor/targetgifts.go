package monitor

import "strings"

type targetGiftProgressKey struct {
	user   string
	gift   string
	target string
}

func targetGiftQuantity(settings Settings, target string) int {
	if quantity := settings.TargetGiftQuantities[target]; quantity > 0 {
		return quantity
	}
	return 1
}

// targetGiftPriority reports whether the gift matches a target that was
// marked as "fura fila" in the settings.
func targetGiftPriority(settings Settings, giftName string) bool {
	for _, target := range settings.TargetGifts {
		if matchesTargetGift(giftName, target) && settings.TargetGiftPriorities[target] {
			return true
		}
	}
	return false
}

// targetGiftPriority is the locked view of the settings helper above.
func (m *Monitor) targetGiftPriority(giftName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return targetGiftPriority(m.settings, giftName)
}

// setCurrentLiveLocked preserves progress on reconnects to the same live.
func (m *Monitor) setCurrentLiveLocked(username string) {
	if normalizeID(m.currentUsername) != normalizeID(username) {
		m.targetGiftProgress = nil
		// O id da sessão anterior pertence a outro streamer: não pode vazar
		// para as escritas da nova live.
		m.liveID = ""
	}
	m.currentUsername = username
}

// consumeTargetGift counts only settled gifts, under the monitor lock. One
// qualifying batch refreshes the participant's row; unused gifts carry forward.
func (m *Monitor) consumeTargetGift(user, giftName string, count int) (bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if user == "" {
		return false, false
	}
	for _, target := range m.settings.TargetGifts {
		if !matchesTargetGift(giftName, target) {
			continue
		}
		quantity := targetGiftQuantity(m.settings, target)
		key := targetGiftProgressKey{user: user, gift: strings.ToLower(giftName), target: target}
		if count < 1 {
			count = 1
		}
		// Split quotient and remainder before adding to avoid integer overflow.
		previous := m.targetGiftProgress[key]
		remainder := count % quantity
		reached := count >= quantity || remainder >= quantity-previous
		if remainder >= quantity-previous {
			remainder -= quantity - previous
		} else {
			remainder += previous
		}
		if m.targetGiftProgress == nil {
			m.targetGiftProgress = make(map[targetGiftProgressKey]int)
		}
		if remainder == 0 {
			delete(m.targetGiftProgress, key)
		} else {
			m.targetGiftProgress[key] = remainder
		}
		return reached, m.pinnedUsers[user]
	}
	return false, false
}
