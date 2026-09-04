/**
 * Type definitions mirroring the backend API contracts
 * (see backend/internal/model and backend/internal/monitor).
 */

export interface State {
  connected: boolean;
  username: string;
  settings: Settings;
  reconnectAttempts?: number;
}

export interface Settings {
  moderationEnabled: boolean;
  logLevel: string;
  targetGifts: string[];
}

export interface Gift {
  id: number;
  live_name: string;
  uniqueId: string;
  nickname: string;
  giftName: string;
  repeatCount: number;
  giftType: number;
  timestamp: string;
}

export interface GoalMilestone {
  atUnits: number;
  reward: string;
  unlocked: boolean;
  unlockedAt?: string;
}

export interface GiftGoal {
  id: number;
  liveName: string;
  title: string;
  giftName?: string;
  targetUnits: number;
  status: string;
  milestones: GoalMilestone[];
  completedAt?: string;
  createdAt: string;
}

export interface GoalProgress {
  goal: GiftGoal;
  units: number;
  percent: number;
}

export interface GoalsState {
  liveName: string;
  active?: GoalProgress;
  actives: GoalProgress[];
  history: GiftGoal[];
}

export interface UserRank {
  uniqueId: string;
  nickname: string;
  score: number;
  giftScore: number;
  diamonds: number;
  tier?: string;
  messageCount: number;
  questionCount: number;
  giftCount: number;
  shareCount: number;
  likeCount: number;
  anomalyCount: number;
  riskLevel: string;
  firstSeen: string;
  lastSeen: string;
}

export interface LiveRanking {
  liveName: string;
  updatedAt: string;
  totalUsers: number;
  userRanks: UserRank[];
  mode?: string;
  totalGiftValue?: number;
  totalLikes?: number;
}

export interface PinnedComment {
  id: number;
  liveName: string;
  uniqueId: string;
  nickname: string;
  comment: string;
  pinId?: string;
  isFollower?: boolean;
  timestamp: string;
}

export interface TargetGiftHistory {
  id: number;
  liveName: string;
  uniqueId: string;
  nickname: string;
  giftName: string;
  receivedAt: string;
  answeredAt?: string;
  responseType?: string;
}

export interface AuthTheme {
  pink: string;
  cyan: string;
  bg: string;
}

/** Response of GET /api/auth/config. */
export interface AuthConfig {
  enabled: boolean;
  supabaseUrl?: string;
  supabaseAnonKey?: string;
  maxLoginAttempts?: number;
  lockoutMinutes?: number;
  theme?: AuthTheme;
}

/** Response of GET /api/auth/me. */
export interface AuthMe {
  authenticated: boolean;
  authEnabled: boolean;
  id?: string;
  email?: string;
  role?: string;
  active?: boolean;
  displayName?: string;
  notes?: string;
  subscriptionExpiresAt?: string;
}

export interface Session {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  token_type?: string;
}

/** Response of POST /api/auth/login. */
export interface AuthLoginResponse {
  session: Session;
}

/** Lockout fields returned by the backend on failed/locked logins. */
export interface LockoutStatus {
  locked?: boolean;
  retryAfterSec?: number;
  remainingAttempts?: number;
  maxAttempts?: number;
}

/** Supabase token-refresh response (grant_type=refresh_token). */
export interface SupabaseRefreshResponse {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  token_type?: string;
  user?: unknown;
}

export type RankingMode = 'engagement' | 'tiktok';

export type TargetGiftResponse = 'manual' | 'automatic';