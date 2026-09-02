package monitor

import (
	"log"
	"strings"
	"time"
)

// sessionReusable is true when last activity is on the same UTC calendar day
// and less than sessionReuseMaxAge before now.
// Timestamps are stored and read in UTC, so compare days in UTC.
func sessionReusable(last, now time.Time) bool {
	if last.IsZero() {
		return false
	}
	last = last.UTC()
	now = now.UTC()
	if last.Year() != now.Year() || last.YearDay() != now.YearDay() {
		return false
	}
	return now.Sub(last) < sessionReuseMaxAge
}

// restoreOrPurgeSessionData reloads today's session if it is still reusable;
// otherwise it deletes gifts, messages and anomaly logs for this live.
func (m *Monitor) restoreOrPurgeSessionData() {
	m.mu.Lock()
	liveName := m.currentUsername
	m.mu.Unlock()

	last, ok, err := m.repo.GetLastSessionActivity(liveName)
	if err != nil {
		log.Printf("[Monitor] Error reading last session activity: %v", err)
		return
	}
	if ok && sessionReusable(last, time.Now()) {
		m.loadTodayData()
		return
	}
	if err := m.repo.DeleteSessionData(liveName); err != nil {
		log.Printf("[Monitor] Error deleting stale session data: %v", err)
		return
	}
	log.Printf("[Monitor] Purged session data for %s", liveName)
}

// loadTodayData loads today's user messages and anomaly logs from the database
// to restore the chat buffer and pinned users when reconnecting to the same live.
func (m *Monitor) loadTodayData() {
	now := time.Now().UnixMilli()

	m.mu.Lock()
	currentUsername := m.currentUsername
	m.mu.Unlock()

	todayMsgs, err := m.repo.GetTodayUserMessages(currentUsername)
	if err != nil {
		log.Printf("[Monitor] Error loading today's messages: %v", err)
	} else if len(todayMsgs) > 0 {
		m.mu.Lock()
		for _, um := range todayMsgs {
			if um.LiveName != "" && !strings.EqualFold(um.LiveName, currentUsername) {
				continue
			}
			ts := parseStoredTimestampMillis(um.Timestamp, now)
			m.chatBuffer = append(m.chatBuffer, ChatMessage{
				UniqueID:  um.UniqueID,
				Nickname:  um.Username,
				Comment:   um.Message,
				Timestamp: ts,
			})
			if looksLikeQuestion(um.Message) {
				m.questionBuffer = append(m.questionBuffer, QuestionEntry{
					UniqueID:  um.UniqueID,
					Nickname:  um.Username,
					Comment:   um.Message,
					Timestamp: ts,
				})
			}
		}
		if len(m.chatBuffer) > chatBufferMax {
			m.chatBuffer = m.chatBuffer[len(m.chatBuffer)-chatBufferMax:]
		}
		if len(m.questionBuffer) > questionBufferMax {
			m.questionBuffer = m.questionBuffer[len(m.questionBuffer)-questionBufferMax:]
		}
		m.mu.Unlock()
		log.Printf("[Monitor] Loaded %d messages from today", len(todayMsgs))
	}

	todayAnomalies, err := m.repo.GetTodayAnomalyLogs(currentUsername)
	if err != nil {
		log.Printf("[Monitor] Error loading today's anomaly logs: %v", err)
		return
	}
	if len(todayAnomalies) == 0 {
		return
	}
	m.mu.Lock()
	for _, al := range todayAnomalies {
		if al.UniqueID != "" {
			m.pinnedUsers[normalizeID(al.UniqueID)] = true
		}
	}
	m.mu.Unlock()
	log.Printf("[Monitor] Restored %d pinned users from today's anomaly logs", len(todayAnomalies))
}
