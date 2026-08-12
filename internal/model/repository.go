// Package model defines repository interfaces for data access.
package model

import "errors"

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
	AddUserMessageDedup(uniqueID, username, message string) error
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

// Repository combines all repository interfaces.
type Repository interface {
	FeedbackRepository
	AnomalyRepository
	UserMessageRepository
	GiftRepository
	Close() error
}
