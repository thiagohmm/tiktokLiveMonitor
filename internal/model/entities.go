// Package model contains data structures and repository interfaces for the application.
package model

// AnomalyLog represents a single moderation record.
type AnomalyLog struct {
	ID        int64  `json:"id"`
	LiveName  string `json:"live_name"`
	Day       string `json:"day"`
	Timestamp string `json:"timestamp"`
	UniqueID  string `json:"uniqueId"`
	Comment   string `json:"comment"`
	IsAnomaly bool   `json:"is_anomaly"`
	Category  string `json:"category"`
}

// UserMessage represents a user message from a live stream.
type UserMessage struct {
	ID        int64  `json:"id"`
	LiveName  string `json:"liveName,omitempty"`
	UniqueID  string `json:"uniqueId"`
	Username  string `json:"username"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// Gift represents a gift received during a live stream.
type Gift struct {
	ID          int64  `json:"id"`
	LiveName    string `json:"live_name"`
	UniqueID    string `json:"uniqueId"`
	Nickname    string `json:"nickname"`
	GiftName    string `json:"giftName"`
	RepeatCount int    `json:"repeatCount"`
	GiftType    int    `json:"giftType"`
	Timestamp   string `json:"timestamp"`
}

// Target gift response types.
const (
	TargetGiftResponseManual    = "manual"
	TargetGiftResponseAutomatic = "automatic"
)

// TargetGiftHistory tracks when a target gift was received and answered.
type TargetGiftHistory struct {
	ID           int64   `json:"id"`
	LiveName     string  `json:"liveName"`
	UniqueID     string  `json:"uniqueId"`
	Nickname     string  `json:"nickname"`
	GiftName     string  `json:"giftName"`
	ReceivedAt   string  `json:"receivedAt"`
	AnsweredAt   *string `json:"answeredAt,omitempty"`
	ResponseType *string `json:"responseType,omitempty"`
}

// PinnedComment is a comment pinned during a live stream.
type PinnedComment struct {
	ID         int64  `json:"id"`
	LiveName   string `json:"liveName"`
	UniqueID   string `json:"uniqueId"`
	Nickname   string `json:"nickname"`
	Comment    string `json:"comment"`
	PinID      string `json:"pinId,omitempty"`
	IsFollower *bool  `json:"isFollower,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// Risk level constants used across ranking and profiles.
const (
	RiskLevelNone    = "none"
	RiskLevelLow     = "low"
	RiskLevelMedium  = "medium"
	RiskLevelHigh    = "high"
	RiskLevelCritical = "critical"
)

// UserRank is a single participant's engagement ranking for a live.
type UserRank struct {
	UniqueID      string  `json:"uniqueId"`
	Nickname      string  `json:"nickname"`
	Score         float64 `json:"score"`
	GiftScore     float64 `json:"giftScore"`
	MessageCount  int     `json:"messageCount"`
	QuestionCount int     `json:"questionCount"`
	GiftCount     int     `json:"giftCount"`
	ShareCount    int     `json:"shareCount"`
	LikeCount     int     `json:"likeCount"`
	AnomalyCount  int     `json:"anomalyCount"`
	RiskLevel     string  `json:"riskLevel"`
	FirstSeen     string  `json:"firstSeen"`
	LastSeen      string  `json:"lastSeen"`
}

// LiveRanking is the full engagement ranking for a single live.
type LiveRanking struct {
	LiveName   string     `json:"liveName"`
	UpdatedAt  string     `json:"updatedAt"`
	TotalUsers int        `json:"totalUsers"`
	UserRanks  []UserRank `json:"userRanks"`
}

// LiveReport is the AI-generated post-live summary.
type LiveReport struct {
	LiveName         string                `json:"liveName"`
	StartedAt        string                `json:"startedAt"`
	EndedAt          string                `json:"endedAt"`
	DurationMinutes  int                   `json:"durationMinutes"`
	MessageCount     int                   `json:"messageCount"`
	ParticipantCount int                   `json:"participantCount"`
	GiftCount        int                   `json:"giftCount"`
	GiftTotal        int                   `json:"giftTotal"`
	TopSupporters    []UserRank            `json:"topSupporters"`
	FrequentQuestions []string             `json:"frequentQuestions"`
	ModerationIssues []AnomalySummary      `json:"moderationIssues"`
	Summary          string                `json:"summary"`
}

// AnomalySummary groups anomaly logs by category for the report.
type AnomalySummary struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// SuggestedResponse is an AI-prepared reply for a question (not auto-published).
type SuggestedResponse struct {
	Question  string `json:"question"`
	Suggested string `json:"suggested"`
	Reason    string `json:"reason"`
}

// UserProfile aggregates a participant's full history across lives.
type UserProfile struct {
	UniqueID      string            `json:"uniqueId"`
	Nickname      string            `json:"nickname"`
	Messages      []UserMessage     `json:"messages"`
	Gifts         []Gift            `json:"gifts"`
	Alerts        []AnomalyLog      `json:"alerts"`
	RiskLevel     string            `json:"riskLevel"`
	TotalMessages int               `json:"totalMessages"`
	TotalGifts    int               `json:"totalGifts"`
	LastLives     []UserLiveSummary `json:"lastLives"`
}

// UserLiveSummary describes one live a participant appeared in.
type UserLiveSummary struct {
	LiveName  string `json:"liveName"`
	Messages  int    `json:"messages"`
	Gifts     int    `json:"gifts"`
	FirstSeen string `json:"firstSeen"`
	LastSeen  string `json:"lastSeen"`
}

// Live is a derived view of one live on one day: activity interval and event count.
// There is no dedicated lives/schedules table; it is aggregated from the tables
// that carry live_name (user_messages, gifts, shares, anomaly_logs, pinned_comments,
// target_gift_history).
type Live struct {
	Name      string `json:"name"`
	Day       string `json:"day"`
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt"`
	Events    int    `json:"events"`
}
