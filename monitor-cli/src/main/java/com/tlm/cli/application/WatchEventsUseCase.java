package com.tlm.cli.application;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.ServerEvent;
import com.tlm.cli.domain.model.UserSession;
import com.tlm.cli.domain.port.EventStreamListener;
import com.tlm.cli.domain.port.EventStreamPort;
import com.tlm.cli.domain.port.StreamHandle;

import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Loop de escuta SSE com reconexão automática: 401 → recuperação de sessão
 * (1 prompt de senha), 503 → espera com backoff, EOF/falha → backoff. Após
 * conectar, re-sincroniza o estado via REST e reseta o backoff.
 */
public final class WatchEventsUseCase implements AutoCloseable {

    private final EventStreamPort ssePort;
    private final MonitorHub hub;
    private final SessionManager sessions;
    private final ReconnectPolicy policy = new ReconnectPolicy();
    private final ExecutorService executor;
    private final Sleeper sleeper;

    private volatile StreamHandle activeHandle;
    private volatile boolean running;
    private volatile CountDownLatch cycleEnd = new CountDownLatch(1);

    public WatchEventsUseCase(EventStreamPort ssePort, MonitorHub hub, SessionManager sessions,
                              ExecutorService executor, Sleeper sleeper) {
        this.ssePort = ssePort;
        this.hub = hub;
        this.sessions = sessions;
        this.executor = executor;
        this.sleeper = sleeper;
    }

    public void start() {
        if (running) {
            return;
        }
        running = true;
        cycleEnd = new CountDownLatch(1);
        executor.submit(this::loop);
    }

    @Override
    public void close() {
        stop();
    }

    public void stop() {
        running = false;
        StreamHandle handle = activeHandle;
        if (handle != null) {
            handle.close();
        }
        cycleEnd.countDown();
    }

    public boolean isRunning() {
        return running;
    }

    private void loop() {
        while (running) {
            if (!hasAuthentication()) {
                hub.alert("Sem sessão válida; escuta encerrada.");
                break;
            }
            CountDownLatch end = new CountDownLatch(1);
            cycleEnd = end;
            CycleListener listener = new CycleListener(end);
            activeHandle = null;
            try {
                activeHandle = ssePort.open(sessionForCycle(), listener);
            } catch (ApiException refused) {
                listener.onFailed(refused);
            }
            if (!running) {
                // stop() pode ter corrido enquanto open() bloqueava: fecha o
                // stream recém-aberto para não vazar a sessão SSE local.
                StreamHandle abandoned = activeHandle;
                activeHandle = null;
                if (abandoned != null) {
                    abandoned.close();
                }
                break;
            }
            try {
                end.await();
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                break;
            }
            if (!running) {
                break;
            }
            ApiException failure = listener.failure();
            if (failure != null && failure.status() == 401) {
                hub.alert("Sessão expirada.");
                if (!sessions.recoverAfter401()) {
                    hub.alert("Re-login cancelado; escuta encerrada.");
                    break;
                }
            } else if (failure != null) {
                hub.alert(ApiErrors.describe(failure));
            } else {
                hub.streamDown("conexão encerrada pelo servidor");
            }
            waitBackoff();
        }
    }

    private boolean hasAuthentication() {
        return sessions.current().isPresent() || sessions.recoverAfter401();
    }

    private UserSession sessionForCycle() {
        UserSession session = sessions.current().orElse(null);
        if (session == null) {
            throw new IllegalStateException("sem sessão para abrir o stream");
        }
        return session;
    }

    private void waitBackoff() {
        long delay = policy.nextDelayMillis();
        hub.notice(String.format("Reconectando em %ds...", delay / 1000));
        try {
            sleeper.sleep(delay);
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        }
    }

    /** Desfechos de UM ciclo de stream; resetam o backoff quando conecta. */
    private final class CycleListener implements EventStreamListener {

        private final CountDownLatch end;
        private final AtomicReference<ApiException> failure = new AtomicReference<>();

        private CycleListener(CountDownLatch end) {
            this.end = end;
        }

        ApiException failure() {
            return failure.get();
        }

        @Override
        public void onConnected() {
            policy.reset();
            hub.streamUp();
            hub.refreshState();
        }

        @Override
        public void onEvent(ServerEvent event) {
            hub.observe(event);
        }

        @Override
        public void onClosed() {
            end.countDown();
        }

        @Override
        public void onFailed(ApiException error) {
            failure.compareAndSet(null, error);
            hub.streamDown(error.getMessage());
            end.countDown();
        }
    }
}
