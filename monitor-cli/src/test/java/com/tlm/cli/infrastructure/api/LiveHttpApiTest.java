package com.tlm.cli.infrastructure.api;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.LiveEntry;
import com.tlm.cli.domain.model.LiveMonitorState;
import com.tlm.cli.domain.model.PinnedCommentEntry;
import com.tlm.cli.domain.model.UserSession;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.net.http.HttpClient;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

class LiveHttpApiTest {

    private MockWebServer server;
    private LiveHttpApi api;
    private UserSession session;

    @BeforeEach
    void start() throws Exception {
        server = new MockWebServer();
        server.start();
        api = new LiveHttpApi(server.url("/").toString(), ApiHttp.client());
        session = new UserSession("TOK-1", "CSRF-1");
    }

    @AfterEach
    void stop() throws Exception {
        server.shutdown();
    }

    private String origin() {
        // Url MockWebServer termina com "/"; o adapter corta.
        return server.url("/").toString().replaceAll("/$", "");
    }

    @Test
    void stateParsesPayload() throws Exception {
        server.enqueue(new MockResponse().setBody("{\"connected\":true,\"username\":\"user5\",\"liveId\":\"7172\",\"settings\":{}}"));

        LiveMonitorState state = api.state(session);

        assertTrue(state.connected());
        assertEquals("user5", state.username());
        assertEquals("7172", state.liveId());
        assertEquals("@user5", state.displayName());
    }

    @Test
    void livesParsesWrapper() throws Exception {
        server.enqueue(new MockResponse().setBody("{\"lives\":[{\"live\":\"user5\",\"state\":\"on\"},{\"live\":\"user7\",\"state\":\"off\"}]}"));

        List<LiveEntry> lives = api.lives(session);

        assertEquals(2, lives.size());
        assertEquals(new LiveEntry("user5", "on"), lives.get(0));
        assertEquals("user7", lives.get(1).live());
        RecordedRequest request = server.takeRequest();
        assertEquals("GET", request.getMethod());
        assertEquals("Bearer TOK-1", request.getHeader("Authorization"));
        assertEquals("/api/lives", request.getPath());
        assertNull(request.getHeader("X-CSRF-Token"), "GET não manda CSRF");
    }

    @Test
    void connectSendsPostBodyOriginAndCsrf() throws Exception {
        server.enqueue(new MockResponse().setBody("{\"success\":true}"));

        api.connect(session, "user5");

        RecordedRequest request = server.takeRequest();
        assertEquals("/api/connect", request.getPath());
        assertEquals("POST", request.getMethod());
        assertEquals("Bearer TOK-1", request.getHeader("Authorization"));
        assertEquals(origin(), request.getHeader("Origin"));
        assertEquals("CSRF-1", request.getHeader("X-CSRF-Token"));
        String body = request.getBody().readUtf8();
        assertEquals("{\"username\":\"user5\"}", body);
        assertEquals("application/json", request.getHeader("Content-Type"));
    }

    @Test
    void connectStripsTrailingSlashFromBaseUrl() throws Exception {
        api = new LiveHttpApi(server.url("/").toString().replaceAll("/$", "") + "//", ApiHttp.client());
        server.enqueue(new MockResponse().setBody("{\"success\":true}"));
        api.connect(session, "user8");
        RecordedRequest request = server.takeRequest();
        assertTrue(request.getPath().startsWith("/api/connect"), request.getPath());
    }

    @Test
    void disconnectUsesQueryParam() throws Exception {
        server.enqueue(new MockResponse().setBody("{\"success\":true}"));

        api.disconnect(session, "user7");

        RecordedRequest request = server.takeRequest();
        assertEquals("/api/disconnect?username=user7", request.getPath());
        assertEquals("POST", request.getMethod());
        assertEquals(origin(), request.getHeader("Origin"));
        assertEquals("CSRF-1", request.getHeader("X-CSRF-Token"));
    }

    @Test
    void disconnectWithoutUsernameOmitsQuery() throws Exception {
        server.enqueue(new MockResponse().setBody("{\"success\":true}"));

        api.disconnect(session, "");

        RecordedRequest request = server.takeRequest();
        assertEquals("/api/disconnect", request.getPath());
    }

    @Test
    void csrf403IsDetectedForRetry() {
        server.enqueue(new MockResponse()
                .setResponseCode(403)
                .setBody("{\"error\":\"token CSRF inválido\"}"));

        ApiException error = assertThrows(ApiException.class, () -> api.connect(session, "user5"));
        assertTrue(error.isCsrfError(), "403 CSRF deve ser reconhecível");
    }

    @Test
    void origin403IsNotCsrf() {
        server.enqueue(new MockResponse()
                .setResponseCode(403)
                .setBody("{\"error\":\"origem não autorizada\"}"));

        ApiException error = assertThrows(ApiException.class, () -> api.connect(session, "user5"));
        assertTrue(!error.isCsrfError());
    }

    @Test
    void orgLimit409Propagates() {
        server.enqueue(new MockResponse()
                .setResponseCode(409)
                .setBody("{\"error\":\"Limite de lives simultâneas da organização atingido.\"}"));

        ApiException error = assertThrows(ApiException.class, () -> api.connect(session, "user5"));
        assertEquals(409, error.status());
    }

    @Test
    void availableGiftsFetchesWithQueryParamAndDedups() throws Exception {
        server.enqueue(new MockResponse().setBody("[\"Rosa\",\"Dino\",\"Rosa\"]"));

        List<String> gifts = api.availableGifts(session, "user 5");

        assertEquals(List.of("Rosa", "Dino"), gifts);
        RecordedRequest request = server.takeRequest();
        assertEquals("GET", request.getMethod());
        assertEquals("/api/available-gifts?live=user+5", request.getPath());
        assertEquals("Bearer TOK-1", request.getHeader("Authorization"));
        assertNull(request.getHeader("X-CSRF-Token"), "GET não manda CSRF");
        assertNull(request.getHeader("Origin"), "GET não manda Origin");
    }

    @Test
    void availableGiftsMalformedArrayIsEmpty() throws Exception {
        server.enqueue(new MockResponse().setBody("{\"surpresa\":\"não é array\"}"));

        assertTrue(api.availableGifts(session, "user5").isEmpty());
        RecordedRequest request = server.takeRequest();
        assertTrue(request.getPath().startsWith("/api/available-gifts?live="), request.getPath());
    }

    @Test
    void availableGiftsEmptyLiveStillQueries() throws Exception {
        server.enqueue(new MockResponse().setBody("[]"));

        assertTrue(api.availableGifts(session, "").isEmpty());
        RecordedRequest request = server.takeRequest();
        assertEquals("/api/available-gifts?live=", request.getPath());
    }

    @Test
    void pinnedCommentsSendsLimit20AndParsesEntries() throws Exception {
        server.enqueue(new MockResponse().setBody(
                "[{\"id\":1,\"liveName\":\"user5\",\"uniqueId\":\"u1\",\"nickname\":\"Ana\","
                        + "\"comment\":\"bom dia\",\"pinId\":\"77\",\"isFollower\":true,"
                        + "\"timestamp\":\"2026-10-06T14:32:00Z\"},"
                        + "{\"id\":2,\"liveName\":\"user5\",\"uniqueId\":\"u2\",\"comment\":\"obrigado\","
                        + "\"timestamp\":\"2026-10-06T14:40:00-03:00\"}]"));

        List<PinnedCommentEntry> entries = api.pinnedComments(session, "user5", 20);

        assertEquals(2, entries.size());
        PinnedCommentEntry first = entries.get(0);
        assertEquals("user5", first.liveName());
        assertEquals("u1", first.uniqueId());
        assertEquals("Ana", first.nickname());
        assertEquals("bom dia", first.comment());
        assertEquals("2026-10-06T14:32:00Z", first.timestamp());
        PinnedCommentEntry second = entries.get(1);
        assertEquals("", second.nickname());
        assertEquals("obrigado", second.comment());
        RecordedRequest request = server.takeRequest();
        assertEquals("GET", request.getMethod());
        assertEquals("/api/pinned-comments?live=user5&limit=20", request.getPath());
        assertEquals("Bearer TOK-1", request.getHeader("Authorization"));
        assertNull(request.getHeader("X-CSRF-Token"), "GET não manda CSRF");
    }

    @Test
    void pinnedCommentsMalformedArrayIsEmpty() throws Exception {
        server.enqueue(new MockResponse().setBody("não-json"));

        assertTrue(api.pinnedComments(session, "user5", 20).isEmpty());
    }
}
