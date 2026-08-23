// Package model defines repository interfaces for data access.
package model

import (
	"errors"
	"time"
)

// ErrCommentRequired is returned when comment is empty.
var ErrCommentRequired = errors.New("comment is required")

// ErrInvalidCategory is returned when category is invalid.
var ErrInvalidCategory = errors.New("invalid category")

// ErrInvalidExpected is returned when expected value is invalid.
var ErrInvalidExpected = errors.New("invalid expected")

// ErrInvalidID is returned when ID is invalid.
var ErrInvalidID = errors.New("invalid id")

// ErrUniqueIDRequired is returned when uniqueId is required but empty.
var ErrUniqueIDRequired = errors.New("uniqueId is required")

// FeedbackRepository handles persistence of user feedback.
type FeedbackRepository interface {
	AddFeedback(comment, category, expected string) (int64, error)
	GetRecentFeedbacks(limit int) ([]Feedback, error)
}

// AnomalyRepository handles persistence of moderation logs.
type AnomalyRepository interface {
	LogAnomaly(liveName, comment string, isAnomaly bool, category, uniqueID string) error
	GetRecentModerations(limit int) ([]AnomalyLog, error)
	GetRecentAnomalyLogs(limit int) ([]AnomalyLog, error)
	GetAnomalyLogsByLiveName(liveName string) ([]AnomalyLog, error)
	GetTodayAnomalyLogs(liveName string) ([]AnomalyLog, error)
	ClearHistory() (int64, error)
	DeleteModeration(id int64) (int64, error)
	CleanupOldAnomalies() (int64, error)
}

// UserMessageRepository handles persistence of user messages.
type UserMessageRepository interface {
	AddUserMessageDedup(liveName, uniqueID, username, message string) error
	GetUserMessages(uniqueID string) ([]UserMessage, error)
	GetAllUserMessages() (map[string][]UserMessage, error)
	GetTodayUserMessages() ([]UserMessage, error)
}

// GiftRepository handles persistence of gifts.
type GiftRepository interface {
	AddGift(liveName, uniqueID, nickname, giftName string, repeatCount, giftType int) (int64, error)
	GetRecentGifts(liveName string, limit int) ([]Gift, error)
	GetGiftsByUser(uniqueID string) ([]Gift, error)
	GetGiftSummary() (map[string]map[string]int, error)
	ClearGifts() (int64, error)
}

// TargetGiftHistoryRepository tracks target gift receive/answer history.
type TargetGiftHistoryRepository interface {
	AddTargetGiftHistory(liveName, uniqueID, nickname, giftName string, receivedAt time.Time) (int64, error)
	MarkTargetGiftAnswered(id int64, responseType string, answeredAt time.Time) error
	GetRecentTargetGiftHistory(liveName string, limit int) ([]TargetGiftHistory, error)
	GetPendingTargetGiftHistory(liveName string, limit int) ([]TargetGiftHistory, error)
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
	FirstSeen     string
	LastSeen      string
}

// Repository combines all repository interfaces.
type Repository interface {
	FeedbackRepository
	AnomalyRepository
	UserMessageRepository
	GiftRepository
	ShareRepository
	TargetGiftHistoryRepository
	PinnedCommentRepository
	SessionRepository
	RankingRepository
	Close() error
}
