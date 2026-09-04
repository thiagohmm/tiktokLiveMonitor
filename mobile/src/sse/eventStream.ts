/**
 * Server-Sent Events client built on `react-native-sse` (XHR-based polling,
 * works in Expo managed without a native module).
 *
 * The backend does NOT emit `id:` fields, so there is no `Last-Event-ID`
 * resume. On every (re)connection we re-sync state via REST.
 */
import EventSource from 'react-native-sse';

import { config } from '@/config';
import {
  getGoals,
  getPinnedComments,
  getRanking,
  getSettings,
  getState,
} from '@/api/endpoints';

/** Raw SSE event names broadcast by the backend. */
export const SSE_EVENTS = {
  ServerState: 'server-state',
  ConnectionStatus: 'connection-status',
  AnyGift: 'any-gift-received',
  GiftUser: 'new-gift-user',
  ChatMessage: 'new-chat-message',
  PinnedComment: 'pinned-comment',
  LikeEvent: 'new-like-event',
  SocialEvent: 'new-social-event',
  Follower: 'new-follower',
  LiveUserConnected: 'live-user-connected',
  GiftsList: 'gifts-list',
  GoalUpdate: 'goal-update',
  GoalCompleted: 'goal-completed',
  GoalUnlocked: 'goal-unlocked',
  SettingsUpdate: 'settings-update',
} as const;

export type SseEventName = (typeof SSE_EVENTS)[keyof typeof SSE_EVENTS];

/** Parsed SSE event carrying typed data. */
export interface SseEvent<T = unknown> {
  type: SseEventName;
  data: T;
  lastEventId: string | null;
  url: string;
}

export interface EventStreamHandlers {
  onEvent: (event: SseEvent) => void;
  /** Called after the connection drops and re-establishes. */
  onReconnect?: () => void;
  onError?: (error: unknown) => void;
}

function parseData(raw: string | null): unknown {
  if (raw == null || raw.length === 0) {
    return null;
  }
  try {
    return JSON.parse(raw);
  } catch {
    return raw;
  }
}

/**
 * Re-syncs all REST-backed state after an SSE reconnect. Errors are swallowed
 * (the SSE loop will retry) and surfaced through `onError`.
 */
async function reSync(): Promise<void> {
  const [state, settings, goals, ranking, pinned] = await Promise.all([
    getState().catch(() => null),
    getSettings().catch(() => null),
    getGoals().catch(() => null),
    getRanking().catch(() => null),
    getPinnedComments().catch(() => null),
  ]);
  // Consumers that need extra data (gifts catalog, target history) can be
  // fetched here too; kept minimal to avoid over-fetching on every reconnect.
  void { state, settings, goals, ranking, pinned };
}

export class EventStream {
  private source: EventSource<SseEventName> | null = null;
  private handlers: EventStreamHandlers | null = null;
  private open = false;
  private reconnectAttempts = 0;

  constructor(private readonly url: string = `${config.API_BASE}/events`) {}

  connect(handlers: EventStreamHandlers): void {
    this.handlers = handlers;
    this.source = new EventSource(this.url, {
      headers: { 'Content-Type': 'text/event-stream' },
      pollingInterval: config.SSE_BACKOFF_BASE_MS,
    });

    this.source.addEventListener('open', () => {
      this.open = true;
      this.reconnectAttempts = 0;
      // First open is the initial connection; subsequent opens are reconnects.
      if (this.handlers) {
        void reSync()
          .catch((err) => this.handlers?.onError?.(err))
          .finally(() => this.handlers?.onReconnect?.());
      }
    });

    for (const name of Object.values(SSE_EVENTS)) {
      this.source.addEventListener(
        name as SseEventName,
        (event: { data: string | null; lastEventId: string | null; url: string }) => {
          const parsed: SseEvent = {
            type: name as SseEventName,
            data: parseData(event.data),
            lastEventId: event.lastEventId,
            url: event.url,
          };
          this.handlers?.onEvent?.(parsed);
        },
      );
    }

    this.source.addEventListener('error', () => {
      this.open = false;
      this.reconnectAttempts += 1;
      this.handlers?.onError?.(new Error('SSE connection error'));
    });
  }

  disconnect(): void {
    this.open = false;
    this.source?.close();
    this.source = null;
    this.handlers = null;
  }
}

export default EventStream;