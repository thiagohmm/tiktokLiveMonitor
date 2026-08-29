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

// FeedbackRepository handles the read-only consumption of the user feedback
// persisted by the Python agent (feedback.db is owned by the agent since the
// AI unification — see docs/plano-unificacao-ia.md).
type FeedbackRepository interface {
	GetFalsePositiveComments(limit int) ([]string, error)
}

// AnomalyRepository handles persistence of moderation logs.
type AnomalyRepository interface {
	LogAnomaly(liveName, comment string, isAnomaly bool, category, uniqueID string) error
	GetRecentModerations(limit int) ([]AnomalyLog, error)
	GetRecentAnomalyLogs(limit int) ([]AnomalyLog, error)
	GetAnomalyLogsByLiveName(liveName string) ([]AnomalyLog, error)
	// GetAnomalyLogsByUser returns anomaly logs for a participant (case-insensitive).
	GetAnomalyLogsByUser(uniqueID string, limit int) ([]AnomalyLog, error)
	GetTodayAnomalyLogs(liveName string) ([]AnomalyLog, error)
	ClearHistory() (int64, error)
	DeleteModeration(id int64) (int64, error)
	CleanupOldAnomalies() (int64, error)
}

// UserMessageRepository handles persistence of user messages.
type UserMessageRepository interface {
	AddUserMessageDedup(liveName, uniqueID, username, message string) error
	GetUserMessages(uniqueID string) ([]UserMessage, error)
	// GetUserMessagesRecent returns the last `limit` messages of a user
	// (newest first).
	GetUserMessagesRecent(uniqueID string, limit int) ([]UserMessage, error)
	GetAllUserMessages() (map[string][]UserMessage, error)
	GetTodayUserMessages(liveName string) ([]UserMessage, error)
}

// GiftRepository handles persistence of gifts.
type GiftRepository interface {
	AddGift(liveName, uniqueID, nickname, giftName string, repeatCount, giftType int) (int64, error)
	GetRecentGifts(liveName string, limit int) ([]Gift, error)
	GetGiftsByUser(uniqueID string) ([]Gift, error)
	GetGiftSummary() (map[string]map[string]int, error)
	// GetGiftUnits returns total gift units (SUM repeat_count) and event count
	// for a live. When no gift names are given, all gifts count.
	GetGiftUnits(liveName string, giftNames ...string) (units, count int, err error)
	ClearGifts() (int64, error)
}

// TargetGiftHistoryRepository tracks target gift receive/answer history.
type TargetGiftHistoryRepository interface {
	AddTargetGiftHistory(liveName, uniqueID, nickname, giftName string, receivedAt time.Time) (int64, error)
	MarkTargetGiftAnswered(id int64, responseType string, answeredAt time.Time) error
	GetRecentTargetGiftHistory(liveName string, limit int) ([]TargetGiftHistory, error)
	GetPendingTargetGiftHistory(liveName string, limit int) ([]TargetGiftHistory, error)
}

// GoalRepository handles persistence of live gift goals.
type GoalRepository interface {
	AddGiftGoal(g GiftGoal) (int64, error)
	GetGiftGoals(liveName string) ([]GiftGoal, error)
	SaveGiftGoal(g GiftGoal) error
	DeleteGiftGoals(liveName string) (int64, error)
}

// PinnedCommentRepository tracks comments pinned during a live.
type PinnedCommentRepository interface {
	AddPinnedComment(liveName, uniqueID, nickname, comment, pinID string, isFollower *bool, at time.Time) (int64, error)
	GetRecentPinnedComments(liveName string, limit int) ([]PinnedComment, error)
}

// SessionRepository handles reuse or purge of live session data on connect.
type SessionRepository interface {
	GetLastSessionActivity(liveName string) (time.Time, bool, error)
	DeleteSessionData(liveName string) error
}

// ShareRepository tracks social shares of the live made by participants.
type ShareRepository interface {
	AddShare(liveName, uniqueID, nickname string) error
	// GetUserShareCount returns the total number of share events made by a user.
	GetUserShareCount(uniqueID string) (int, error)
}

// LikeRepository tracks likes (hearts) sent by participants during a live.
type LikeRepository interface {
	AddLike(liveName, uniqueID, nickname string, likeCount int) error
	// GetUserLikeTotal returns the sum of like_count over all like events of a user.
	GetUserLikeTotal(uniqueID string) (int64, error)
	// UpsertRoomLikeTotal stores the room-level cumulative like counter as
	// reported by the stream (monotonic: only the highest value is kept).
	UpsertRoomLikeTotal(liveName string, total int64) error
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
	// ListLives returns derived lives grouped by live_name and day, most recent first.
	ListLives(limit int) ([]Live, error)

	// DeleteLive removes all stored rows for a live (across every table with live_name).
	DeleteLive(liveName string) (int64, error)
}

// LiveStat is per-user aggregated data used to compute a ranking score.
type LiveStat struct {
	UniqueID      string
	Nickname      string
	MessageCount  int
	QuestionCount int
	GiftCount     int
	GiftTotal     int
	ShareCount    int
	LikeCount     int
	FirstSeen     string
	LastSeen      string
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
