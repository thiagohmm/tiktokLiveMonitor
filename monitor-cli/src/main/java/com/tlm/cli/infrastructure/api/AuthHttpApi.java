package com.tlm.cli.infrastructure.api;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.UserSession;
import com.tlm.cli.domain.port.AuthPort;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * Adaptador de autenticação: login extrai o token do header
 * {@code Set-Cookie: tlm_session=...} (o corpo só tem {@code csrfToken}) e
 * mantém tudo em memória.
 */
public final class AuthHttpApi implements AuthPort {

    static final String SESSION_COOKIE = "tlm_session";

    private final URI base;
    private final HttpClient client;

    public AuthHttpApi(String baseUrl, HttpClient client) {
        this.base = URI.create(ApiHttp.uriOf(baseUrl, ""));
        this.client = client;
    }

    @Override
    public UserSession login(String email, char[] password) throws ApiException {
        Map<String, Object> body = new HashMap<>();
        body.put("email", email == null ? "" : email.trim());
        body.put("password", password == null ? "" : new String(password));
        HttpRequest request = HttpRequest.newBuilder(URI.create(base + "/api/auth/login"))
                .timeout(Duration.ofSeconds(30))
                .header("Content-Type", "application/json")
                // O middleware exige Origin == SITE_URL em TODO método não-GET
                // (inclusive login público); CSRF só depois do PublicPath.
                .header("Origin", base.toString())
                .POST(HttpRequest.BodyPublishers.ofString(ApiHttp.jsonOf(body)))
                .build();
        HttpResponse<String> response = send("/api/auth/login", request);
        Map<String, Object> payload = ApiHttp.parseObject(response.body());

        if (Boolean.TRUE.equals(payload.get("needsActivation"))) {
            Object message = payload.get("message");
            throw new ApiException(200, message instanceof String m ? m : "conta migrada", payload);
        }
        if (response.statusCode() != 200) {
            throw ApiHttp.errorFrom(response.statusCode(), response.body());
        }
        String token = sessionToken(response);
        String csrf = stringOf(payload, "csrfToken");
        if (csrf.isBlank()) {
            throw new ApiException(200, "resposta de login sem csrfToken", payload);
        }
        return new UserSession(token, csrf);
    }

    @Override
    public UserSession me(UserSession session) throws ApiException {
        HttpRequest request = HttpRequest.newBuilder(URI.create(base + "/api/auth/me"))
                .timeout(Duration.ofSeconds(30))
                .header("Authorization", "Bearer " + session.accessToken())
                .GET()
                .build();
        HttpResponse<String> response = send("/api/auth/me", request);
        if (response.statusCode() != 200) {
            throw ApiHttp.errorFrom(response.statusCode(), response.body());
        }
        Map<String, Object> payload = ApiHttp.parseObject(response.body());
        String csrf = stringOf(payload, "csrfToken");
        if (csrf.isBlank()) {
            throw new ApiException(200, "resposta /me sem csrfToken", payload);
        }
        return session.withCsrfToken(csrf);
    }

    private String sessionToken(HttpResponse<String> response) throws ApiException {
        List<String> setCookies = response.headers().allValues("set-cookie");
        for (String header : setCookies) {
            String trimmed = header.trim();
            if (trimmed.startsWith(SESSION_COOKIE + "=")) {
                String value = trimmed.substring((SESSION_COOKIE + "=").length());
                int end = value.indexOf(';');
                if (end >= 0) {
                    value = value.substring(0, end);
                }
                value = value.trim();
                if (!value.isBlank()) {
                    return value;
                }
            }
        }
        throw new ApiException(200, "resposta sem cookie " + SESSION_COOKIE, Map.of());
    }

    private HttpResponse<String> send(String path, HttpRequest request) throws ApiException {
        try {
            return client.send(request, HttpResponse.BodyHandlers.ofString());
        } catch (IOException e) {
            throw ApiHttp.network(e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new ApiException(ApiException.STATUS_NETWORK, "interrompido", Map.of(), e);
        }
    }

    private static String stringOf(Map<String, Object> payload, String key) {
        Object value = payload.get(key);
        return value == null ? "" : String.valueOf(value);
    }
}
