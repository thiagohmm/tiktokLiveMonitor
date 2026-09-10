// Package model defines repository interfaces for data access.
package model

import (
	"errors"
	"time"
)

// ErrCommentRequired is returned when comment is empty.
var ErrCommentRequired = errors.New("comment is required")

// ErrInvalidID is returned when ID is invalid.
var ErrInvalidID = errors.New("invalid id")

// ErrUniqueIDRequired is returned when uniqueId is required but empty.
var ErrUniqueIDRequired = errors.New("uniqueId is required")

// ErrLiveSessionNotFound is returned when a live session id does not exist.
var ErrLiveSessionNotFound = errors.New("live session not found")

// FeedbackRepository handles the read-only consumption of the user feedback
// persisted by the Python agent (feedback.db is owned by the agent since the
// AI unification — see docs/plano-unificacao-ia.md).
type FeedbackRepository interface {
	GetFalsePositiveComments(limit int) ([]string, error)
}

// AnomalyRepository handles persistence of moderation logs.
type AnomalyRepository interface {
	LogAnomaly(ref LiveRef, comment string, isAnomaly bool, category, uniqueID string) error
	GetRecentModerations(limit int) ([]AnomalyLog, error)
	GetRecentAnomalyLogs(limit int) ([]AnomalyLog, error)
	GetAnomalyLogsByLiveName(liveName string) ([]AnomalyLog, error)
	// GetAnomalyLogsByUser returns anomaly logs for a participant (case-insensitive).
	GetAnomalyLogsByUser(uniqueID string, limit int) ([]AnomalyLog, error)
	GetSessionAnomalyLogs(liveID string) ([]AnomalyLog, error)
	ClearHistory() (int64, error)
	DeleteModeration(id int64) (int64, error)
	CleanupOldAnomalies() (int64, error)
}

// UserMessageRepository handles persistence of user messages.
type UserMessageRepository interface {
	AddUserMessageDedup(ref LiveRef, uniqueID, username, message string) error
	GetUserMessages(uniqueID string) ([]UserMessage, error)
	// GetUserMessagesRecent returns the last `limit` messages of a user
	// (newest first).
	GetUserMessagesRecent(uniqueID string, limit int) ([]UserMessage, error)
	GetAllUserMessages() (map[string][]UserMessage, error)
	GetSessionUserMessages(liveID string) ([]UserMessage, error)
}

// GiftRepository handles persistence of gifts.
type GiftRepository interface {
	AddGift(ref LiveRef, uniqueID, nickname, giftName string, repeatCount, giftType int) (int64, error)
	GetRecentGifts(liveName string, limit int) ([]Gift, error)
	GetGiftsByUser(uniqueID string) ([]Gift, error)
	GetGiftSummary() (map[string]map[string]int, error)
	// GetGiftUnits returns total gift units (SUM repeat_count) and event count
	// for a session. When no gift names are given, all gifts count.
	GetGiftUnits(ref LiveRef, giftNames ...string) (units, count int, err error)
	ClearGifts() (int64, error)
}

// TargetGiftHistoryRepository tracks target gift receive/answer history.
type TargetGiftHistoryRepository interface {
	AddTargetGiftHistory(ref LiveRef, uniqueID, nickname, giftName string, receivedAt time.Time, priority bool) (int64, error)
	MarkTargetGiftAnswered(id int64, responseType string, answeredAt time.Time) error
	// SetTargetGiftPriority promotes (priority=true) or demotes (priority=false)
	// a pending entry in the gift queue. Promotion stamps `at` as the
	// promotion moment (FIFO among jumpers); demotion clears it.
	SetTargetGiftPriority(id int64, priority bool, at time.Time) error
	GetRecentTargetGiftHistory(liveName string, limit int) ([]TargetGiftHistory, error)
	GetPendingTargetGiftHistory(liveName string, limit int) ([]TargetGiftHistory, error)
}

// GoalRepository handles persistence of live gift goals. Goals belong to a
// session: a goal created during a live is deleted with that live.
type GoalRepository interface {
	AddGiftGoal(g GiftGoal) (int64, error)
	GetGiftGoals(ref LiveRef) ([]GiftGoal, error)
	SaveGiftGoal(g GiftGoal) error
}

// PinnedCommentRepository tracks comments pinned during a live.
type PinnedCommentRepository interface {
	AddPinnedComment(ref LiveRef, uniqueID, nickname, comment, pinID string, isFollower *bool, at time.Time) (int64, error)
	GetRecentPinnedComments(liveName string, limit int) ([]PinnedComment, error)
}

// SessionRepository handles the lifecycle of monitoring sessions. A session is
// one connection to a live; live_name alone (the streamer username) is shared
// by every session of that streamer and cannot identify one.
type SessionRepository interface {
	// BeginLiveSession resumes an open, still-reusable session for liveName or
	// starts a new one. It never deletes data.
	BeginLiveSession(liveName string, now time.Time) (LiveSession, error)
	// EndLiveSession closes a session. Idempotent: closing twice keeps the
	// first ended_at instead of erroring.
	EndLiveSession(id string, at time.Time) error
	// TouchLiveSession refreshes last_seen_at (used by the resume rule).
	TouchLiveSession(id string, at time.Time) error
	GetLiveSession(id string) (LiveSession, error)
	// LatestLiveSession resolves a session from a streamer name (open session
	// first, then most recent). Used for events that arrive without a liveId.
	LatestLiveSession(liveName string) (LiveSession, error)
	// DeleteLiveSession removes every row produced by one session and returns
	// the total number of rows deleted.
	DeleteLiveSession(id string) (int64, error)
}

// ShareRepository tracks social shares of the live made by participants.
type ShareRepository interface {
	AddShare(ref LiveRef, uniqueID, nickname string) error
	// GetUserShareCount returns the total number of share events made by a user.
	GetUserShareCount(uniqueID string) (int, error)
}

// LikeRepository tracks likes (hearts) sent by participants during a live.
type LikeRepository interface {
	AddLike(ref LiveRef, uniqueID, nickname string, likeCount int) error
	// GetUserLikeTotal returns the sum of like_count over all like events of a user.
	GetUserLikeTotal(uniqueID string) (int64, error)
	// UpsertRoomLikeTotal stores the room-level cumulative like counter as
	// reported by the stream (monotonic: only the highest value is kept).
	UpsertRoomLikeTotal(ref LiveRef, total int64) error
	// LikeTotals returns the room-level cumulative like total and the sum of
	// the per-event likes actually delivered by the stream for a live.
	LikeTotals(liveName string) (roomTotal, delivered int64, err error)
}

// RankingRepository handles engagement ranking and live analytics.
type RankingRepository interface {
	// LiveFirstSeen returns the first recorded timestamp for a live (RFC3339).
	LiveFirstSeen(liveName string) (string, error)
	// LiveStatsByUser returns per-user aggregated stats for a live.
	LiveStatsByUser(liveName string) ([]LiveStat, error)
	// RecentLivesForUser returns the last N lives a participant appeared in.
	RecentLivesForUser(uniqueID string, limit int) ([]UserLiveSummary, error)
	// TotalDistinctUsers counts distinct users across all user_messages.
	TotalDistinctUsers() (int, error)
	// ListLives returns one row per session (live connection), most recent first.
	ListLives(limit int) ([]Live, error)
}

// LiveStat is per-user aggregated data used to compute a ranking score.
type LiveStat struct {
	UniqueID      string
	Nickname      string
	MessageCount  int
	QuestionCount int
	GiftCount     int
	// GiftTotal is the sum of gift units (repeat_count) sent by the user.
	GiftTotal int
	// GiftValue is the total coin (diamond) value of the gifts sent by the
	// user: sum of GiftValue(gift_name) x repeat_count, using the researched
	// TikTok live gift price table.
	GiftValue  int
	ShareCount int
	LikeCount  int
	FirstSeen  string
	LastSeen   string
}

// SettingsRepository persists application settings (target gifts, moderation
// toggles, etc.) as key/value entries so they survive restarts.
type SettingsRepository interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

// Repository combines all repository interfaces.
type Repository interface {
	FeedbackRepository
	AnomalyRepository
	UserMessageRepository
	GiftRepository
	ShareRepository
	LikeRepository
	TargetGiftHistoryRepository
	GoalRepository
	PinnedCommentRepository
	SessionRepository
	RankingRepository
	SettingsRepository
	Close() error
}
