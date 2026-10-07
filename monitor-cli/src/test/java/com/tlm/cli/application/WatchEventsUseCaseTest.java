package com.tlm.cli.application;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.LiveMonitorState;
import com.tlm.cli.domain.model.ServerEvent;
import com.tlm.cli.domain.model.UserSession;
import com.tlm.cli.domain.port.AuthPort;
import com.tlm.cli.domain.port.EventStreamListener;
import com.tlm.cli.domain.port.EventStreamPort;
import com.tlm.cli.domain.port.LiveApiPort;
import com.tlm.cli.domain.port.PasswordSource;
import com.tlm.cli.domain.port.StreamHandle;
import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.ArrayDeque;
import java.util.Deque;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class WatchEventsUseCaseTest {

    private static class FakeAuth implements AuthPort {
        final AtomicInteger logins = new AtomicInteger();
        final AtomicReference<String> lastEmail = new AtomicReference<>();
        final AtomicReference<char[]> lastPassword = new AtomicReference<>();

        @Override
        public UserSession login(String email, char[] password) {
            logins.incrementAndGet();
            lastEmail.set(email);
            lastPassword.set(password.clone());
            return new UserSession("tok-" + logins.get(), "csrf-" + logins.get());
        }

        @Override
        public UserSession me(UserSession session) {
            return session.withCsrfToken("csrf-refreshed");
        }
    }

    /** Auth cujo /me sempre falha 401 (sessão realmente revogada). */
    private static class DeadSessionAuth implements AuthPort {
        final AtomicInteger logins = new AtomicInteger();

        @Override
        public UserSession login(String email, char[] password) {
            logins.incrementAndGet();
            return new UserSession("tok-fresh-" + logins.get(), "csrf-fresh");
        }

        @Override
        public UserSession me(UserSession session) throws ApiException {
            throw new ApiException(401, "não autorizado", Map.of());
        }
    }

    /** Porta SSE falsa: cada open() consome um roteiro de desfecho. */
    private static class ScriptedSse implements EventStreamPort {
        sealed interface Outcome permits Failure, Stream {
        }

        record Failure(ApiException error) implements Outcome {
        }

        record Stream(List<ServerEvent> events) implements Outcome {
        }

        final Deque<Outcome> script = new ArrayDeque<>();
        final AtomicInteger opens = new AtomicInteger();

        @Override
        public StreamHandle open(UserSession session, EventStreamListener listener) throws ApiException {
            opens.incrementAndGet();
            Outcome outcome = script.peekFirst();
            if (outcome instanceof Failure failure) {
                script.pollFirst();
                throw failure.error();
            }
            if (outcome instanceof Stream stream) {
                script.pollFirst();
                listener.onConnected();
                for (ServerEvent event : stream.events()) {
                    listener.onEvent(event);
                }
                listener.onClosed();
            }
            return () -> {
            };
        }
    }

    private static class FakeLiveApi implements LiveApiPort {
        final AtomicInteger states = new AtomicInteger();

        @Override
        public LiveMonitorState state(UserSession session) throws ApiException {
            states.incrementAndGet();
            return new LiveMonitorState(true, "user5", "7172");
        }

        @Override
        public java.util.List<com.tlm.cli.domain.model.LiveEntry> lives(UserSession session) {
            return List.of();
        }

        @Override
        public void connect(UserSession session, String username) {
        }

        @Override
        public void disconnect(UserSession session, String username, java.time.Duration timeout) {
        }

        @Override
        public java.util.List<String> availableGifts(UserSession session, String live) {
            return List.of();
        }

        @Override
        public java.util.List<com.tlm.cli.domain.model.PinnedCommentEntry> pinnedComments(
                UserSession session, String live, int limit) {
            return List.of();
        }
    }

    private static class Recorder implements MonitorListener {
        final List<DomainEvent> events = new CopyOnWriteArrayList<>();
        final List<LiveMonitorState> states = new CopyOnWriteArrayList<>();
        final List<String> notices = new CopyOnWriteArrayList<>();
        final List<String> alerts = new CopyOnWriteArrayList<>();
        final AtomicInteger streamUps = new AtomicInteger();

        @Override
        public void onDomainEvent(DomainEvent event) {
            events.add(event);
        }

        @Override
        public void onStateChanged(LiveMonitorState state) {
            states.add(state);
        }

        @Override
        public void onNotice(String message) {
            notices.add(message);
        }

        @Override
        public void onAlert(String message) {
            alerts.add(message);
        }

        @Override
        public void onStreamUp() {
            streamUps.incrementAndGet();
        }
    }

    private final ExecutorService executor = Executors.newVirtualThreadPerTaskExecutor();
    private final List<Long> sleeps = new CopyOnWriteArrayList<>();
    private final Sleeper sleeper = millis -> sleeps.add(millis);

    private static LiveMonitorState onlineState(boolean connected, String user) {
        return new LiveMonitorState(connected, user, "");
    }

    private static ServerEvent eventOf(String type, Map<String, Object> data) {
        return new ServerEvent(type, data, Instant.parse("2026-10-06T15:02:03Z"));
    }

    private Testable build(AuthPort auth, PasswordSource passwords, ScriptedSse sse,
                           LiveApiPort api, Recorder listener) {
        return build(auth, passwords, sse, api, listener, sleeper);
    }

    private Testable build(AuthPort auth, PasswordSource passwords, ScriptedSse sse,
                           LiveApiPort api, Recorder listener, Sleeper sleeperForLoop) {
        SessionManager sessions = new SessionManager(auth, passwords);
        sessions.setEmail("eu@example.org");
        MonitorHub hub = new MonitorHub(api, sessions);
        hub.addListener(listener);
        WatchEventsUseCase watch = new WatchEventsUseCase(sse, hub, sessions, executor, sleeperForLoop);
        return new Testable(sessions, watch, sse, (FakeLiveApi) api);
    }

    private record Testable(SessionManager sessions, WatchEventsUseCase watch,
                            ScriptedSse sse, FakeLiveApi api) {
        void startAndWait(int expectedOpens, long timeoutMillis) throws InterruptedException {
            watch.start();
            long deadline = System.currentTimeMillis() + timeoutMillis;
            while (sse.opens.get() < expectedOpens && System.currentTimeMillis() < deadline) {
                Thread.sleep(10);
            }
        }
    }

    @Test
    void serverStateFrameUpdatesHeaderState() throws Exception {
        FakeAuth auth = new FakeAuth();
        ScriptedSse sse = new ScriptedSse();
        FakeLiveApi api = new FakeLiveApi();
        Recorder listener = new Recorder();
        Testable env = build(auth, password -> Optional.empty(), sse, api, listener);
        env.sessions.login("eu@example.org", "senha".toCharArray());

        sse.script.add(new ScriptedSse.Stream(List.of(
                eventOf("server-state", Map.of("connected", true, "username", "user5")),
                eventOf("new-chat-message", Map.of("uniqueId", "u1", "comment", "oi")))));
        try {
            env.startAndWait(1, 5000);
            long deadline = System.currentTimeMillis() + 5000;
            while (listener.states.isEmpty() && System.currentTimeMillis() < deadline) {
                Thread.sleep(10);
            }
            assertTrue(!listener.states.isEmpty(), "server-state deve virar estado do header");
            assertTrue(listener.states.get(0).connected());
            assertEquals("user5", listener.states.get(0).username());
            assertEquals(1, listener.events.size(), "evento de chat decodificado deve chegar");
        } finally {
            env.watch.stop();
        }
    }

    @Test
    void fiveOhThreeThenSuccessBacksOffWithPolicyAndResets() throws Exception {
        FakeAuth auth = new FakeAuth();
        ScriptedSse sse = new ScriptedSse();
        FakeLiveApi api = new FakeLiveApi();
        Recorder listener = new Recorder();
        Testable env = build(auth, password -> Optional.empty(), sse, api, listener);
        env.sessions.login("eu@example.org", "senha".toCharArray());

        // F503 → backoff 1s; F503 → 2s; sucesso conecta (reset) e depois cai → 1s; F503 → 2s.
        sse.script.add(new ScriptedSse.Failure(new ApiException(503, "muitos clientes", Map.of())));
        sse.script.add(new ScriptedSse.Failure(new ApiException(503, "muitos clientes", Map.of())));
        sse.script.add(new ScriptedSse.Stream(List.of(eventOf("server-state", Map.of()))));
        sse.script.add(new ScriptedSse.Failure(new ApiException(503, "muitos clientes", Map.of())));
        try {
            env.watch.start();
            awaitCondition(() -> sleeps.size() >= 4, 10000);
            assertEquals(List.of(1000L, 2000L, 1000L, 2000L), sleeps,
                    "backoff segue política e reseta após conexão bem-sucedida");
            assertEquals(1, env.api.states.get(), "após conectar usa GET /api/state");
            assertEquals(1, listener.streamUps.get(), "só a tentativa bem-sucedida é 'stream up'");
        } finally {
            env.watch.stop();
        }
    }

    @Test
    void fortyOneRecoversWithPasswordAndContinues() throws Exception {
        DeadSessionAuth auth = new DeadSessionAuth();
        ScriptedSse sse = new ScriptedSse();
        FakeLiveApi api = new FakeLiveApi();
        Recorder listener = new Recorder();
        List<char[]> promptPasswords = new CopyOnWriteArrayList<>();
        Testable env = build(auth, info -> {
            promptPasswords.add("nova-senha".toCharArray());
            return Optional.of("nova-senha".toCharArray());
        }, sse, api, listener);
        env.sessions.login("eu@example.org", "velha".toCharArray());

        sse.script.add(new ScriptedSse.Failure(new ApiException(401, "não autorizado", Map.of())));
        sse.script.add(new ScriptedSse.Stream(List.of(eventOf("server-state", Map.of()))));
        try {
            env.watch.start();
            awaitCondition(() -> !promptPasswords.isEmpty() && env.sse.opens.get() >= 2, 10000);
            assertEquals(1, promptPasswords.size(), "um prompt de senha (recuperação 401)");
            assertEquals(2, auth.logins.get(), "login inicial + re-login após 401");
            assertEquals(1, listener.streamUps.get(), "segunda tentativa conecta com a nova sessão");
        } finally {
            env.watch.stop();
        }
    }

    @Test
    void stopEndsTheLoopWithoutFurtherOpens() throws Exception {
        FakeAuth auth = new FakeAuth();
        ScriptedSse sse = new ScriptedSse();
        FakeLiveApi api = new FakeLiveApi();
        Recorder listener = new Recorder();
        // Sleeper real: sem roteiro o ciclo fica aguardando o latch até o stop().
        Testable env = build(auth, password -> Optional.empty(), sse, api, listener, Thread::sleep);
        env.sessions.login("eu@example.org", "senha".toCharArray());

        env.startAndWait(1, 5000);
        env.watch.stop();

        Thread.sleep(300);
        assertEquals(1, env.sse.opens.get(), "depois de stop() não deve abrir de novo");
    }

    @Test
    void backoffSequenceFollowsPolicyCap() throws Exception {
        FakeAuth auth = new FakeAuth();
        ScriptedSse sse = new ScriptedSse();
        FakeLiveApi api = new FakeLiveApi();
        Recorder listener = new Recorder();
        Testable env = build(auth, password -> Optional.empty(), sse, api, listener);
        env.sessions.login("eu@example.org", "senha".toCharArray());
        sse.script.add(new ScriptedSse.Failure(new ApiException(503, "cap", Map.of())));
        sse.script.add(new ScriptedSse.Failure(new ApiException(503, "cap", Map.of())));
        sse.script.add(new ScriptedSse.Failure(new ApiException(503, "cap", Map.of())));
        sse.script.add(new ScriptedSse.Failure(new ApiException(503, "cap", Map.of())));
        sse.script.add(new ScriptedSse.Failure(new ApiException(503, "cap", Map.of())));
        try {
            env.watch.start();
            awaitCondition(() -> sleeps.size() >= 5, 10000);
            assertEquals(List.of(1000L, 2000L, 4000L, 8000L, 15000L), sleeps,
                    "1s → ×2 → teto 15s espelhando frontend/auth.js");
        } finally {
            env.watch.stop();
        }
    }

    private static void awaitCondition(java.util.function.BooleanSupplier condition, long timeoutMillis)
            throws InterruptedException {
        long deadline = System.currentTimeMillis() + timeoutMillis;
        while (!condition.getAsBoolean() && System.currentTimeMillis() < deadline) {
            Thread.sleep(10);
        }
        if (!condition.getAsBoolean()) {
            throw new AssertionError("condição não atingida em " + timeoutMillis + "ms");
        }
    }
}
