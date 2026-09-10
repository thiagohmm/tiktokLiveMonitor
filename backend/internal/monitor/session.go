package monitor

import (
	"errors"
	"log"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// sessionTouchMinInterval throttles last_seen_at writes. The resume rule and the
// listing of open sessions only need hour granularity, so one write per minute
// per live is enough to keep them honest without amplifying event I/O.
const sessionTouchMinInterval = time.Minute

// beginOrResumeSession opens the live session for the current streamer.
//
// It resumes the session still open from a previous backend run when it is
// recent enough, and starts a new id otherwise. Nothing is deleted: the buffers
// are restored from this session's rows only, so data from other lives never
// leaks into the current one and nothing has to be purged.
func (m *Monitor) beginOrResumeSession() {
	m.mu.Lock()
	liveName := m.currentUsername
	repo := m.repo
	m.mu.Unlock()

	if repo == nil || liveName == "" {
		return
	}

	session, err := repo.BeginLiveSession(liveName, time.Now())
	if err != nil {
		log.Printf("[Monitor] Error starting live session for %s: %v", liveName, err)
		return
	}

	m.mu.Lock()
	m.liveID = session.ID
	m.mu.Unlock()

	m.loadSessionData(session.ID)
	log.Printf("[Monitor] Live session %s started for %s", session.ID, liveName)
}

// endCurrentSession closes the current session, if any. Safe to call on every
// shutdown path (StopMonitoring and Close both run during single-mode shutdown).
func (m *Monitor) endCurrentSession() {
	m.mu.Lock()
	liveID := m.liveID
	repo := m.repo
	m.liveID = ""
	m.mu.Unlock()

	if repo == nil || liveID == "" {
		return
	}
	if err := repo.EndLiveSession(liveID, time.Now()); err != nil {
		log.Printf("[Monitor] Error ending live session %s: %v", liveID, err)
	}
}

// touchSession refreshes last_seen_at at most once per sessionTouchMinInterval.
//
// It doubles as the health check of the session: when the session was deleted
// from the administration while the live is still streaming, a new one is
// opened here, so the following events belong to a live that the admin can see
// and delete again (instead of piling up invisible orphan rows).
func (m *Monitor) touchSession() {
	m.mu.Lock()
	liveID := m.liveID
	repo := m.repo
	if repo == nil || liveID == "" || time.Since(m.liveTouchAt) < sessionTouchMinInterval {
		m.mu.Unlock()
		return
	}
	m.liveTouchAt = time.Now()
	m.mu.Unlock()

	err := repo.TouchLiveSession(liveID, time.Now())
	if err == nil {
		return
	}
	if errors.Is(err, model.ErrLiveSessionNotFound) {
		log.Printf("[Monitor] Live session %s was deleted; opening a new session", liveID)
		m.beginOrResumeSession()
		return
	}
	log.Printf("[Monitor] Error touching live session %s: %v", liveID, err)
}

// loadSessionData restores the chat buffer and pinned users from the rows of
// this session, so a resumed session continues where it stopped.
func (m *Monitor) loadSessionData(liveID string) {
	m.mu.Lock()
	repo := m.repo
	m.mu.Unlock()
	if repo == nil || liveID == "" {
		return
	}

	now := time.Now().UnixMilli()

	sessionMsgs, err := repo.GetSessionUserMessages(liveID)
	if err != nil {
		log.Printf("[Monitor] Error loading session messages: %v", err)
	} else if len(sessionMsgs) > 0 {
		m.mu.Lock()
		for _, um := range sessionMsgs {
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
		log.Printf("[Monitor] Loaded %d messages from session %s", len(sessionMsgs), liveID)
	}

	sessionAnomalies, err := repo.GetSessionAnomalyLogs(liveID)
	if err != nil {
		log.Printf("[Monitor] Error loading session anomaly logs: %v", err)
		return
	}
	if len(sessionAnomalies) == 0 {
		return
	}
	m.mu.Lock()
	for _, al := range sessionAnomalies {
		if al.UniqueID != "" {
			m.pinnedUsers[normalizeID(al.UniqueID)] = true
		}
	}
	m.mu.Unlock()
	log.Printf("[Monitor] Restored %d pinned users from session %s", len(sessionAnomalies), liveID)
}
