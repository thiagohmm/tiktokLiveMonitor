package com.tlm.cli.infrastructure.api;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.UserSession;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.net.http.HttpClient;
import java.util.Map;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

class AuthHttpApiTest {

    private MockWebServer server;
    private AuthHttpApi api;
    private HttpClient client;

    @BeforeEach
    void start() throws Exception {
        server = new MockWebServer();
        server.start();
        client = ApiHttp.client();
        api = new AuthHttpApi(server.url("/").toString(), client);
    }

    @AfterEach
    void stop() throws Exception {
        server.shutdown();
    }

    private String origin() {
        return server.url("/").toString().replaceAll("/$", "");
    }

    private String baseUrl() {
        return server.url("/").toString();
    }

    @Test
    void loginExtractsTokenFromSetCookie() throws Exception {
        server.enqueue(new MockResponse()
                .setHeader("Content-Type", "application/json")
                .addHeader("Set-Cookie", "tlm_session=SECRET123; Path=/; HttpOnly; Max-Age=86400")
                .setBody("{\"authenticated\":true,\"csrfToken\":\"CSRF456\"}"));

        UserSession session = api.login("eu@example.org", "senha".toCharArray());

        assertEquals("SECRET123", session.accessToken());
        assertEquals("CSRF456", session.csrfToken());

        RecordedRequest recorded = server.takeRequest();
        assertEquals("/api/auth/login", recorded.getPath());
        assertEquals("POST", recorded.getMethod());
        String body = recorded.getBody().readUtf8();
        assertTrue(body.contains("\"email\":\"eu@example.org\""), body);
        assertTrue(body.contains("\"password\":\"senha\""), body);
        // Login é público para TOKEN, mas o middleware exige Origin em todo
        // não-GET (auth.go:86-94 antes do bypass PublicPath).
        assertEquals(origin() , recorded.getHeader("Origin"));
        // e não deve enviar Authorization nesse ponto (sem sessão ainda)
        assertNull(recorded.getHeader("Authorization"));
    }

    @Test
    void login401CarriesRemainingAttempts() {
        server.enqueue(new MockResponse()
                .setResponseCode(401)
                .setBody("{\"error\":\"credenciais inválidas\",\"remainingAttempts\":4}"));

        ApiException error = assertThrows(ApiException.class, () -> api.login("eu@example.org", "x".toCharArray()));
        assertEquals(401, error.status());
        assertEquals(Optional.of(4), error.remainingAttempts().map(Number::intValue));
    }

    @Test
    void login429IsLockoutWithRetryAfterSec() {
        server.enqueue(new MockResponse()
                .setResponseCode(429)
                .setBody("{\"error\":\"conta temporariamente bloqueada por excesso de tentativas\",\"locked\":true,\"retryAfterSec\":120,\"remainingAttempts\":0}"));

        ApiException error = assertThrows(ApiException.class, () -> api.login("eu@example.org", "x".toCharArray()));
        assertTrue(error.isLockout());
        assertEquals(Optional.of(120), error.retryAfterSec().map(Number::intValue));
    }

    @Test
    void login403InactiveAccount() {
        server.enqueue(new MockResponse()
                .setResponseCode(403)
                .setBody("{\"error\":\"cadastro aguardando aprovação do administrador após o pagamento\"}"));

        ApiException error = assertThrows(ApiException.class, () -> api.login("eu@example.org", "x".toCharArray()));
        assertEquals(403, error.status());
        assertTrue(error.getMessage().contains("aguardando"));
    }

    @Test
    void loginNeedsActivationBodyMapsToNonRetryable() {
        server.enqueue(new MockResponse()
                .setBody("{\"needsActivation\":true,\"redirectTo\":\"https://site/reset\",\"message\":\"Conta migrada: defina uma nova senha para continuar.\"}"));

        ApiException error = assertThrows(ApiException.class, () -> api.login("eu@example.org", "x".toCharArray()));
        assertTrue(error.isNeedsActivation());
        assertEquals("https://site/reset", error.redirectTo().orElse(""));
    }

    @Test
    void loginWithoutCookieIsAnError() {
        server.enqueue(new MockResponse()
                .setBody("{\"authenticated\":true,\"csrfToken\":\"CSRF456\"}"));

        ApiException error = assertThrows(ApiException.class, () -> api.login("eu@example.org", "x".toCharArray()));
        assertTrue(error.getMessage().contains("tlm_session"), error.getMessage());
    }

    @Test
    void meSendsBearerAndReturnsFreshCsrf() throws Exception {
        server.enqueue(new MockResponse()
                .setBody("{\"csrfToken\":\"CSRF-FRESH\",\"authenticated\":true,\"email\":\"eu@example.org\",\"id\":\"u1\",\"role\":\"operator\",\"active\":true}"));

        UserSession refreshed = api.me(new UserSession("TOK", "OLD-CSRF"));

        assertEquals("CSRF-FRESH", refreshed.csrfToken());
        assertEquals("TOK", refreshed.accessToken());

        RecordedRequest recorded = server.takeRequest();
        assertEquals("/api/auth/me", recorded.getPath());
        assertEquals("Bearer TOK", recorded.getHeader("Authorization"));
    }

    @Test
    void me401Propagates() {
        server.enqueue(new MockResponse()
                .setResponseCode(401)
                .setBody("{\"error\":\"não autorizado\"}"));

        ApiException error = assertThrows(ApiException.class,
                () -> api.me(new UserSession("TOK", "CSRF")));
        assertEquals(401, error.status());
    }

    @Test
    void networkFailureBecomesNetworkStatus() throws Exception {
        server.shutdown();
        ApiException error = assertThrows(ApiException.class, () -> api.login("eu@example.org", "x".toCharArray()));
        assertEquals(ApiException.STATUS_NETWORK, error.status());
    }
}
