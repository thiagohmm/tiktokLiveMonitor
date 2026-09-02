package monitor

import (
	"fmt"
	"log"
	"strings"
	"time"
)

func (m *Monitor) handleBridgeEvent(eventType string, data EventData) {
	switch eventType {
	case "connection-status":
		success, _ := data["success"].(bool)
		m.mu.Lock()
		m.connected = success
		if success {
			m.reconnectAttempts = 0
		}
		stopped := m.userStopped
		m.mu.Unlock()
		log.Printf("[Monitor] Bridge connection-status: success=%v username=%v", success, data["username"])
		m.emit(eventType, data)
		if !success && !stopped {
			select {
			case m.reconnectKick <- struct{}{}:
			default:
			}
		}

	case "new-chat-message":
		m.mu.Lock()
		pending := m.handleChatMessage(data)
		m.mu.Unlock()
		for _, p := range pending {
			m.emit(p.eventType, p.data)
		}
		m.emit(eventType, data)

	case "any-gift-received":
		m.handleGiftReceived(data)

	case "new-gift-user":
		m.handleSettledGiftUser(data)

	case EventNewLike:
		m.emit(EventNewLike, data)

	case "pinned-comment":
		m.mu.Lock()
		user := extractFromData(data)
		key := normalizeID(user.UniqueID)
		m.pinnedUsers[key] = true
		m.mu.Unlock()
		m.emit(eventType, data)
		if user.UniqueID != "" {
			m.emit(EventMarkUserRed, EventData{"uniqueId": key})
		}

	case "live-user-connected", "new-follower", "new-social-event":
		m.emit(eventType, data)

	case "mark-user-red":
		m.emit(eventType, data)

	case "error":
		log.Printf("[Bridge] Error: %v", data["message"])

	case EventGiftsList:
		names := parseGiftNames(data)
		m.cacheAvailableGifts(names)
		select {
		case m.giftsCh <- names:
		default:
		}
		if len(names) > 0 {
			m.emit(EventGiftsList, EventData{"gifts": names})
		}
	}
}

// handleChatMessage muta o estado interno e retorna eventos pendentes.
// DEVE ser chamada com m.mu travado. NUNCA chame m.emit aqui dentro
// (emit trava m.mu e causaria deadlock — sync.Mutex não é reentrante).
func (m *Monitor) handleChatMessage(data EventData) []pendingEmit {
	var pending []pendingEmit

	comment := asString(data["comment"])
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return pending
	}

	user := extractFromData(data)
	now := time.Now().UnixMilli()

	senderKey := normalizeID(user.UniqueID)

	// Comparação byte a byte (cópia e cola exata): mesma caixa e mesmos espaços.
	repeats := 0
	for _, msg := range m.chatBuffer {
		if normalizeID(msg.UniqueID) == senderKey &&
			strings.TrimSpace(msg.Comment) == comment &&
			(now-msg.Timestamp) < repeatWindowMs {
			repeats++
		}
	}

	seqKey := fmt.Sprintf(`["%s","%s"]`, senderKey, comment)
	if repeats >= repeatsRequired-1 {
		if !m.repeatAlerted[seqKey] {
			m.repeatAlerted[seqKey] = true
			pending = append(pending, pendingEmit{EventFlaggedMessage, EventData{
				"uniqueId":   user.UniqueID,
				"nickname":   user.Nickname,
				"isFollower": user.IsFollower,
				"comment":    comment,
				"reason":     "Mensagem repetida",
				"category":   "REPETICAO",
			}})
		}
	} else {
		delete(m.repeatAlerted, seqKey)
	}

	m.chatBuffer = append(m.chatBuffer, ChatMessage{
		UniqueID:   user.UniqueID,
		Nickname:   user.Nickname,
		Comment:    comment,
		Timestamp:  now,
		IsFollower: user.IsFollower,
	})
	if len(m.chatBuffer) > chatBufferMax {
		m.chatBuffer = m.chatBuffer[1:]
	}

	if looksLikeQuestion(comment) {
		m.questionBuffer = append(m.questionBuffer, QuestionEntry{
			UniqueID:   user.UniqueID,
			Nickname:   user.Nickname,
			Comment:    comment,
			Timestamp:  now,
			IsFollower: user.IsFollower,
		})
	}
	m.pruneQuestions(now)

	if keyword := m.detectKeyword(comment); keyword != "" {
		m.pinnedUsers[senderKey] = true
		pending = append(pending, pendingEmit{EventKeywordMention, EventData{
			"uniqueId":   user.UniqueID,
			"nickname":   user.Nickname,
			"comment":    comment,
			"keyword":    keyword,
			"timestamp":  now,
			"isFollower": user.IsFollower,
		}})
		pending = append(pending, pendingEmit{EventMarkUserRed, EventData{"uniqueId": senderKey}})
	}

	return pending
}

func (m *Monitor) handleTargetGift(data EventData) {
	user := extractFromData(data)
	uniqueID := normalizeID(user.UniqueID)

	m.mu.Lock()
	isPinned := m.pinnedUsers[uniqueID]
	m.mu.Unlock()

	giftName := asString(data["giftName"])
	if giftName == "" {
		giftName = asString(data["name"])
	}
	isTarget := m.isTargetGift(giftName)

	data["isRed"] = isTarget && isPinned

	if !isTarget || !m.isGiftCountingSettlement(data) {
		return
	}

	m.emit(EventGiftUser, data)

	repeatCount, _ := toInt(data["repeatCount"])
	giftType, _ := toInt(data["giftType"])
	repeatEnd := truthy(data["repeatEnd"])

	gift := GiftPayload{
		GiftName:    giftName,
		UniqueID:    user.UniqueID,
		Nickname:    user.Nickname,
		RepeatCount: repeatCount,
		RepeatEnd:   repeatEnd,
		GiftType:    giftType,
		IsFollower:  user.IsFollower,
	}
	go m.correlateGiftWithQuestion(gift)
}
