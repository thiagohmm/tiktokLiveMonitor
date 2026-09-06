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

// setCurrentLiveLocked preserves progress on reconnects to the same live.
func (m *Monitor) setCurrentLiveLocked(username string) {
	if normalizeID(m.currentUsername) != normalizeID(username) {
		m.targetGiftProgress = nil
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
