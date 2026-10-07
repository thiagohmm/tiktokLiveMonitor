package com.tlm.cli.application;

/**
 * Backoff exponencial de reconexão SSE, espelhando {@code frontend/auth.js}:
 * 1s → ×2 → teto 15s. {@link #reset()} zera a sequência após uma conexão
 * bem-sucedida.
 */
public final class ReconnectPolicy {

    private static final long BASE_MS = 1000;
    private static final long MAX_MS = 15000;
    private static final int MAX_STEP = 4;

    private int attempt;

    public synchronized long nextDelayMillis() {
        long delay = Math.min(MAX_MS, BASE_MS << Math.min(attempt, MAX_STEP));
        attempt = Math.min(attempt + 1, MAX_STEP);
        return delay;
    }

    public synchronized void reset() {
        attempt = 0;
    }

    public synchronized int attempts() {
        return attempt;
    }
}
