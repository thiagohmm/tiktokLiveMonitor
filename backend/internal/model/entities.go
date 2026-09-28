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
// IsPriority marks the gift as "fura fila" (queue jumper); PriorityAt is the
// promotion moment and orders jumpers FIFO among themselves.
type TargetGiftHistory struct {
	ID           int64   `json:"id"`
	LiveName     string  `json:"liveName"`
	UniqueID     string  `json:"uniqueId"`
	Nickname     string  `json:"nickname"`
	GiftName     string  `json:"giftName"`
	ReceivedAt   string  `json:"receivedAt"`
	AnsweredAt   *string `json:"answeredAt,omitempty"`
	ResponseType *string `json:"responseType,omitempty"`
	IsPriority   bool    `json:"isPriority"`
	PriorityAt   *string `json:"priorityAt,omitempty"`
}

// Gift goal status values.
const (
	GoalStatusActive    = "active"
	GoalStatusCompleted = "completed"
	GoalStatusCancelled = "cancelled"
)

// GoalMilestone is a progress point on a gift goal: when the live passes
// AtUnits units the Reward text is granted.
type GoalMilestone struct {
	AtUnits    int     `json:"atUnits"`
	Reward     string  `json:"reward"`
	Unlocked   bool    `json:"unlocked"`
	UnlockedAt *string `json:"unlockedAt,omitempty"`
}

// GiftGoal is a live gift goal: a target in gift units (SUM repeat_count)
// with text reward milestones. When GiftName is empty the goal counts all
// gifts of the live; otherwise only units of the given gift count.
type GiftGoal struct {
	ID          int64           `json:"id"`
	LiveID      string          `json:"liveId,omitempty"`
	LiveName    string          `json:"liveName"`
	Title       string          `json:"title"`
	GiftName    string          `json:"giftName,omitempty"`
	TargetUnits int             `json:"targetUnits"`
	Status      string          `json:"status"`
	Milestones  []GoalMilestone `json:"milestones"`
	CompletedAt *string         `json:"completedAt,omitempty"`
	CreatedAt   string          `json:"createdAt"`
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
	RiskLevelNone     = "none"
	RiskLevelLow      = "low"
	RiskLevelMedium   = "medium"
	RiskLevelHigh     = "high"
	RiskLevelCritical = "critical"
)

// TikTok-style visual tiers for the in-room gifter ranking (mirrors the
// LIVE Ranking gifts/badges TikTok shows on stream: crown for #1, headband
// for #2, medal for #3).
const (
	TierCrown    = "crown"    // 1st place
	TierHeadband = "headband" // 2nd place
	TierMedal    = "medal"    // 3rd place
)

// Ranking modes.
const (
	ModeEngagement = "engagement" // default weighted engagement score
	ModeTikTok     = "tiktok"     // TikTok in-room ranking: pure gift (diamond) value
)

// UserRank is a single participant's engagement ranking for a live.
type UserRank struct {
	UniqueID  string  `json:"uniqueId"`
	Nickname  string  `json:"nickname"`
	Score     float64 `json:"score"`
	GiftScore float64 `json:"giftScore"`
	// Diamonds is the total coin (diamond) value of the gifts sent by the
	// user (sum of gift price x repeat count), the metric TikTok's in-room
	// live ranking is based on.
	Diamonds int `json:"diamonds"`
	// Tier holds the TikTok visual tier for the gifter ranking top 3
	// (crown / headband / medal); empty outside that podium.
	Tier          string `json:"tier,omitempty"`
	MessageCount  int    `json:"messageCount"`
	QuestionCount int    `json:"questionCount"`
	GiftCount     int    `json:"giftCount"`
	ShareCount    int    `json:"shareCount"`
	LikeCount     int    `json:"likeCount"`
	AnomalyCount  int    `json:"anomalyCount"`
	RiskLevel     string `json:"riskLevel"`
	FirstSeen     string `json:"firstSeen"`
	LastSeen      string `json:"lastSeen"`
}

// LiveRanking is the full engagement ranking for a single live.
type LiveRanking struct {
	LiveName   string     `json:"liveName"`
	UpdatedAt  string     `json:"updatedAt"`
	TotalUsers int        `json:"totalUsers"`
	UserRanks  []UserRank `json:"userRanks"`
	// Mode is the ranking criterion in use ("engagement" or "tiktok").
	Mode string `json:"mode,omitempty"`
	// TotalGiftValue is the room-level sum of gift coin values (💎).
	TotalGiftValue int `json:"totalGiftValue,omitempty"`
	// TotalLikes is the room-level cumulative like counter reported by the
	// TikTok stream (authoritative overall like count for the live).
	TotalLikes int64 `json:"totalLikes,omitempty"`
}

// LiveReport is the AI-generated post-live summary.
type LiveReport struct {
	LiveName         string `json:"liveName"`
	StartedAt        string `json:"startedAt"`
	EndedAt          string `json:"endedAt"`
	DurationMinutes  int    `json:"durationMinutes"`
	MessageCount     int    `json:"messageCount"`
	ParticipantCount int    `json:"participantCount"`
	GiftCount        int    `json:"giftCount"`
	GiftTotal        int    `json:"giftTotal"`
	// GiftValue is the total coin (💎) value of the gifts received in the live.
	GiftValue         int              `json:"giftValue,omitempty"`
	TopSupporters     []UserRank       `json:"topSupporters"`
	FrequentQuestions []string         `json:"frequentQuestions"`
	ModerationIssues  []AnomalySummary `json:"moderationIssues"`
	Summary           string           `json:"summary"`
}

// AnomalySummary groups anomaly logs by category for the report.
type AnomalySummary struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// UserProfile aggregates a participant's full history across lives.
type UserProfile struct {
	UniqueID      string        `json:"uniqueId"`
	Nickname      string        `json:"nickname"`
	Messages      []UserMessage `json:"messages"`
	Gifts         []Gift        `json:"gifts"`
	Alerts        []AnomalyLog  `json:"alerts"`
	RiskLevel     string        `json:"riskLevel"`
	TotalMessages int           `json:"totalMessages"`
	TotalGifts    int           `json:"totalGifts"`
	// TotalGiftUnits is the sum of repeat_count over the user's gifts
	// (same metric used by gift goals and reports).
	TotalGiftUnits int `json:"totalGiftUnits"`
	// TotalGiftValue is the sum of the coin (💎) value of the user's gifts
	// (gift price x repeat_count, researched TikTok live gift table).
	TotalGiftValue int `json:"totalGiftValue,omitempty"`
	// TotalLikes is the sum of like_count over the user's like events.
	TotalLikes int `json:"totalLikes"`
	// TotalShares is the number of share events made by the user.
	TotalShares int               `json:"totalShares"`
	LastLives   []UserLiveSummary `json:"lastLives"`
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
	ID        string `json:"id"`
	Name      string `json:"name"`
	Day       string `json:"day"`
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt"`
	Events    int    `json:"events"`
}

// LiveSession is one monitoring connection to a live. live_name alone is the
// streamer username, so it cannot identify a live: only ID can.
type LiveSession struct {
	ID         string `json:"id"`
	OrgID      string `json:"orgId,omitempty"`
	LiveName   string `json:"name"`
	Day        string `json:"day"`
	StartedAt  string `json:"startedAt"`
	LastSeenAt string `json:"lastSeenAt"`
	EndedAt    string `json:"endedAt,omitempty"`
}

// LiveRef is the (session, streamer) pair carried by every write. Passing both
// together makes it impossible to store an event without its session id.
// OrgID is the organization that owns the session.
type LiveRef struct {
	ID    string
	Name  string
	OrgID string
}

// --- Organizations (tenants) ---

// Organization member roles. The platform admin role lives in the Supabase
// app_metadata and is independent from the role inside an organization.
const (
	OrgRoleOwner    = "owner"
	OrgRoleOperator = "operator"
)

// DefaultOrgID is the organization that receives every row written before
// multi-tenancy existed (and every legacy admin without a membership).
const DefaultOrgID = "00000000-0000-0000-0000-000000000001"

// LegacyOrgName is the name of DefaultOrgID: the legacy organization holding
// data without an identifiable owner. It has no members; only platform
// admins reach it.
const LegacyOrgName = "Legado (somente admin da plataforma)"

// DefaultOrgMaxLives bounds the concurrent lives of a new organization.
const DefaultOrgMaxLives = 3

// Organization is one tenant: its members share settings, lives and Fila PIX.
type Organization struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MaxLives  int    `json:"maxLives"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"createdAt"`
	Members   int    `json:"members"`
}

// OrgMember links one Supabase user to exactly one organization.
type OrgMember struct {
	OrgID     string `json:"orgId"`
	UserID    string `json:"userId"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
}

// LiveAssignment selects legacy sessions to move into a customer organization:
// explicit session ids and/or every legacy session of a streamer (live_name).
type LiveAssignment struct {
	OrgID        string
	SessionIDs   []string
	LiveNames    []string
	CopySettings bool
}

// LiveAssignResult reports what AssignLegacyLives moved.
type LiveAssignResult struct {
	Moved          int64    `json:"moved"`
	LiveNames      []string `json:"liveNames"`
	SettingsCopied bool     `json:"settingsCopied"`
}

// ValidOrgRole reports whether role is a known organization role.
func ValidOrgRole(role string) bool {
	return role == OrgRoleOwner || role == OrgRoleOperator
}

// Valid reports whether the ref points at a session.
func (r LiveRef) Valid() bool {
	return r.ID != ""
}

// --- Fila PIX (WhatsApp/WAHA) ---

// Pix WhatsApp session statuses (normalized from WAHA).
const (
	PixSessionDisconnected = "disconnected"
	PixSessionScanQR       = "scan_qr"
	PixSessionStarting     = "starting"
	PixSessionConnected    = "connected"
	PixSessionFailed       = "failed"
	PixSessionStopped      = "stopped"
)

// Pix ticket statuses.
const (
	PixTicketPending  = "pending"
	PixTicketAnswered = "answered"
)

// Pix message directions.
const (
	PixDirectionInbound  = "inbound"
	PixDirectionOutbound = "outbound"
)

// Pix message types.
const (
	PixTypeText        = "text"
	PixTypeImage       = "image"
	PixTypeDocument    = "document"
	PixTypeUnsupported = "unsupported"
)

// PixWhatsAppSession is the per-user WAHA pairing state.
type PixWhatsAppSession struct {
	ID          int64  `json:"id"`
	OrgID       string `json:"orgId"`
	SessionName string `json:"sessionName"`
	Status      string `json:"status"`
	MePhone     string `json:"mePhone,omitempty"`
	MeJID       string `json:"meJid,omitempty"`
	ConnectedAt string `json:"connectedAt,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// PixContact is a WhatsApp counterpart of one owner's Fila PIX.
type PixContact struct {
	ID             int64  `json:"id"`
	OrgID          string `json:"orgId"`
	PhoneE164      string `json:"phoneE164,omitempty"`
	WhatsAppJID    string `json:"whatsappJid"`
	PushName       string `json:"pushName,omitempty"`
	FirstContactAt string `json:"firstContactAt"`
	LastContactAt  string `json:"lastContactAt"`
}

// PixTicket is one queue item (pending) or a finished conversation (answered).
type PixTicket struct {
	ID                 int64      `json:"id"`
	OrgID              string     `json:"orgId"`
	ContactID          int64      `json:"contactId"`
	Status             string     `json:"status"`
	HasReceipt         bool       `json:"hasReceipt"`
	AutoReplySent      bool       `json:"autoReplySent"`
	ReceivedAt         string     `json:"receivedAt"`
	LastMessageAt      string     `json:"lastMessageAt"`
	AnsweredAt         string     `json:"answeredAt,omitempty"`
	AnsweredBy         string     `json:"answeredBy,omitempty"`
	Contact            PixContact `json:"contact"`
	LastMessagePreview string     `json:"lastMessagePreview,omitempty"`
	LastMessageType    string     `json:"lastMessageType,omitempty"`
	MessageCount       int        `json:"messageCount"`
	ActiveMediaCount   int        `json:"activeMediaCount"`
	// PaidTotalCents sums the values extracted from the ticket's receipts;
	// HasInboundText marks tickets where the client already wrote something.
	// Both drive the value gate that hides partial payments from the queue.
	PaidTotalCents int64 `json:"paidTotalCents"`
	HasInboundText bool  `json:"hasInboundText"`
	// HasUnextractedReceipt marks a ticket whose stored receipts had no
	// readable value (OCR engine down = fail-open). Those tickets stay visible.
	HasUnextractedReceipt bool `json:"-"`
}

// PixMessage is one inbound or outbound WhatsApp message of a ticket.
type PixMessage struct {
	ID                int64  `json:"id"`
	OrgID             string `json:"orgId"`
	TicketID          int64  `json:"ticketId"`
	ContactID         int64  `json:"contactId"`
	WhatsAppMessageID string `json:"whatsappMessageId"`
	Direction         string `json:"direction"`
	Type              string `json:"type"`
	Body              string `json:"body,omitempty"`
	MediaPath         string `json:"-"`
	MediaMime         string `json:"mediaMime,omitempty"`
	MediaFilename     string `json:"mediaFilename,omitempty"`
	MediaDeletedAt    string `json:"mediaDeletedAt,omitempty"`
	MediaDeleteReason string `json:"mediaDeleteReason,omitempty"`
	// MediaValueCents is the amount extracted from a receipt (0 when nothing
	// was extracted); it backs the Fila PIX value gate.
	MediaValueCents int64  `json:"mediaValueCents,omitempty"`
	CreatedAt       string `json:"createdAt"`
}

// MediaAvailable reports whether the receipt file is still stored.
func (m PixMessage) MediaAvailable() bool {
	return m.MediaPath != "" && m.MediaDeletedAt == ""
}
