package com.tlm.cli.infrastructure.api;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.ServerEvent;
import com.tlm.cli.domain.model.UserSession;
import com.tlm.cli.domain.port.EventStreamListener;
import com.tlm.cli.domain.port.StreamHandle;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import okhttp3.mockwebserver.SocketPolicy;
import okio.Buffer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.Timeout;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

class SseClientTest {

    private static final String STREAM = """
            event: server-state
            data: {"connected":false,"username":""}

            : ping

            event: new-chat-message
            data: {"uniqueId":"u1","comment":"oi","orgId":"o1"}

            event: future-thing
            data: x until {"quebra":true

            """;

    private MockWebServer server;
    private SseClient adapter;

    private final BlockingQueue<ServerEvent> events = new ArrayBlockingQueue<>(64);
    private final List<ApiException> failures = new ArrayList<>();
    private CountDownLatch closed = new CountDownLatch(1);
    private final CountDownLatch connected = new CountDownLatch(1);
    private final EventStreamListener listener = new EventStreamListener() {
        @Override
        public void onConnected() {
            connected.countDown();
        }

        @Override
        public void onEvent(ServerEvent event) {
            events.offer(event);
        }

        @Override
        public void onClosed() {
            closed.countDown();
        }

        @Override
        public void onFailed(ApiException error) {
            failures.add(error);
            closed.countDown();
        }
    };

    @BeforeEach
    void start() throws Exception {
        server = new MockWebServer();
        server.start();
        adapter = new SseClient(server.url("/").toString(), ApiHttp.client());
    }

    @AfterEach
    void stop() throws Exception {
        server.shutdown();
    }

    private StreamHandle open() throws ApiException {
        return adapter.open(new UserSession("TOK", "CSRF"), listener);
    }

    @Test
    @Timeout(20)
    void streamsChunkedFramesToListener() throws Exception {
        Buffer body = new Buffer().writeUtf8(STREAM);
        server.enqueue(new MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setChunkedBody(body, 3));

        StreamHandle handle = open();
        assertTrue(connected.await(10, TimeUnit.SECONDS), "onConnected deve disparar após headers 200");

        ServerEvent state = events.poll(5, TimeUnit.SECONDS);
        ServerEvent chat = events.poll(5, TimeUnit.SECONDS);
        ServerEvent future = events.poll(5, TimeUnit.SECONDS);

        assertEquals("server-state", state.type());
        Map<String, Object> stateData = state.data();
        assertFalse((Boolean) stateData.get("connected"));

        assertEquals("new-chat-message", chat.type());
        assertEquals("oi", chat.data().get("comment"));
        // payload aberto: chave de roteamento presente (harmless para exibir, ignorar)
        assertEquals("o1", chat.data().get("orgId"));

        // JSON inválido não quebra: vira payload cru
        assertEquals("future-thing", future.type());
        assertTrue(future.data().containsKey("raw"), "payload não-JSON deve vir cru");

        // EOF limpo do servidor → onClosed
        assertTrue(closed.await(10, TimeUnit.SECONDS), "EOF do stream deve sinalizar onClosed");
        assertTrue(failures.isEmpty(), failures::toString);

        RecordedRequest request = server.takeRequest();
        assertEquals("/events", request.getPath());
        assertEquals("Bearer TOK", request.getHeader("Authorization"));
        assertEquals("GET", request.getMethod());
    }

    @Test
    void refused401ThrowsApiException() throws Exception {
        server.enqueue(new MockResponse()
                .setResponseCode(401)
                .setBody("{\"error\":\"não autorizado\"}"));

        ApiException error = assertThrows(ApiException.class, this::open);
        assertEquals(401, error.status());
    }

    @Test
    void refused503ThrowsApiExceptionWithPlainBody() throws Exception {
        server.enqueue(new MockResponse()
                .setResponseCode(503)
                .setHeader("Content-Type", "text/plain; charset=utf-8")
                .setBody("servidor com muitos clientes conectados; tente novamente em instantes\n"));

        ApiException error = assertThrows(ApiException.class, this::open);
        assertEquals(503, error.status());
        assertTrue(error.getMessage().contains("muitos clientes"), error.getMessage());
    }

    @Test
    void abortDuringResponseBecomesNetworkFailure() throws Exception {
        server.enqueue(new MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setBody("event: server-state\ndata: {\"connected\":false}\n\n")
                .setSocketPolicy(SocketPolicy.DISCONNECT_DURING_RESPONSE_BODY));

        StreamHandle handle = open();
        assertTrue(connected.await(10, TimeUnit.SECONDS));
        assertTrue(closed.await(10, TimeUnit.SECONDS), "abort do stream deve encerrar o ciclo");
        handle.close();
    }

    @Test
    @Timeout(20)
    void closeHandleStopsStreamAndSuppressesOnClosed() throws Exception {
        StringBuilder many = new StringBuilder();
        for (int i = 0; i < 400; i++) {
            many.append("data: {\"i\":").append(i).append("}\n\n");
        }
        server.enqueue(new MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setChunkedBody(new Buffer().writeUtf8(many.toString()), 8)
                .throttleBody(4, 10, TimeUnit.MILLISECONDS));

        StreamHandle handle = open();
        assertTrue(events.poll(10, TimeUnit.SECONDS) != null, "primeiro evento deve chegar");

        handle.close();
        TimeUnit.MILLISECONDS.sleep(500);

        assertEquals(1, closed.getCount(), "close do cliente não deve reportar onClosed nem onFailed");
    }
}
