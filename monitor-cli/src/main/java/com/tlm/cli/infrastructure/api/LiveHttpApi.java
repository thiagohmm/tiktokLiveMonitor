package com.tlm.cli.infrastructure.api;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.LiveEntry;
import com.tlm.cli.domain.model.LiveMonitorState;
import com.tlm.cli.domain.model.PinnedCommentEntry;
import com.tlm.cli.domain.model.UserSession;
import com.tlm.cli.domain.port.LiveApiPort;

import java.io.IOException;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Adaptador REST autenticado: todas as chamadas levam
 * {@code Authorization: Bearer}; as mutações levam também
 * {@code Origin: <base URL>} + {@code X-CSRF-Token} (regra do middleware Go).
 */
public final class LiveHttpApi implements LiveApiPort {

    private final URI base;
    private final HttpClient client;

    public LiveHttpApi(String baseUrl, HttpClient client) {
        this.base = URI.create(ApiHttp.uriOf(baseUrl, ""));
        this.client = client;
    }

    @Override
    public LiveMonitorState state(UserSession session) throws ApiException {
        HttpResponse<String> response = request(session, "/api/state", null, false, "GET", null, DEFAULT_TIMEOUT);
        if (response.statusCode() != 200) {
            throw ApiHttp.errorFrom(response.statusCode(), response.body());
        }
        Map<String, Object> payload = ApiHttp.parseObject(response.body());
        boolean connected = Boolean.TRUE.equals(payload.get("connected"));
        String username = stringOf(payload, "username");
        String liveId = stringOf(payload, "liveId");
        return new LiveMonitorState(connected, username, liveId);
    }

    @Override
    public List<LiveEntry> lives(UserSession session) throws ApiException {
        HttpResponse<String> response = request(session, "/api/lives", null, false, "GET", null, DEFAULT_TIMEOUT);
        if (response.statusCode() != 200) {
            throw ApiHttp.errorFrom(response.statusCode(), response.body());
        }
        Map<String, Object> payload = ApiHttp.parseObject(response.body());
        Object lives = payload.get("lives");
        List<LiveEntry> entries = new ArrayList<>();
        if (lives instanceof List<?> rows) {
            for (Object row : rows) {
                if (row instanceof Map<?, ?> map) {
                    entries.add(new LiveEntry(
                            stringOf(map, "live"),
                            stringOf(map, "state")));
                }
            }
        }
        return entries;
    }

    @Override
    public void connect(UserSession session, String username) throws ApiException {
        Map<String, Object> body = new HashMap<>();
        body.put("username", username);
        HttpResponse<String> response = request(session, "/api/connect", body, true, "POST", null, DEFAULT_TIMEOUT);
        expectSuccess(response, "/api/connect");
    }

    @Override
    public void disconnect(UserSession session, String username, Duration timeout) throws ApiException {
        String query = username == null || username.isBlank()
                ? ""
                : "?username=" + URLEncoder.encode(username, StandardCharsets.UTF_8);
        HttpResponse<String> response = request(session, "/api/disconnect", Map.of(), true, "POST", query, timeout);
        expectSuccess(response, "/api/disconnect");
    }

    @Override
    public List<String> availableGifts(UserSession session, String live) throws ApiException {
        String query = "?live=" + URLEncoder.encode(live == null ? "" : live, StandardCharsets.UTF_8);
        HttpResponse<String> response =
                request(session, "/api/available-gifts", null, false, "GET", query, DEFAULT_TIMEOUT);
        if (response.statusCode() != 200) {
            throw ApiHttp.errorFrom(response.statusCode(), response.body());
        }
        // Deduplica preservando a ordem (o cache do backend pode repetir nomes).
        Set<String> uniques = new LinkedHashSet<>();
        for (Object value : ApiHttp.parseArray(response.body())) {
            if (value instanceof String name && !name.isBlank()) {
                uniques.add(name);
            }
        }
        return List.copyOf(uniques);
    }

    @Override
    public List<PinnedCommentEntry> pinnedComments(UserSession session, String live, int limit) throws ApiException {
        String query = "?live=" + URLEncoder.encode(live == null ? "" : live, StandardCharsets.UTF_8)
                + "&limit=" + Math.max(1, limit);
        HttpResponse<String> response =
                request(session, "/api/pinned-comments", null, false, "GET", query, DEFAULT_TIMEOUT);
        if (response.statusCode() != 200) {
            throw ApiHttp.errorFrom(response.statusCode(), response.body());
        }
        List<PinnedCommentEntry> entries = new ArrayList<>();
        for (Object value : ApiHttp.parseArray(response.body())) {
            if (value instanceof Map<?, ?> map) {
                entries.add(new PinnedCommentEntry(
                        stringOf(map, "liveName"),
                        stringOf(map, "uniqueId"),
                        stringOf(map, "nickname"),
                        stringOf(map, "comment"),
                        stringOf(map, "timestamp")));
            }
        }
        return entries;
    }

    private void expectSuccess(HttpResponse<String> response, String path) throws ApiException {
        if (response.statusCode() != 200) {
            throw ApiHttp.errorFrom(response.statusCode(), response.body());
        }
        Map<String, Object> payload = ApiHttp.parseObject(response.body());
        if (Boolean.FALSE.equals(payload.get("success"))) {
            throw new ApiException(response.statusCode(), "operação recusada em " + path, payload);
        }
    }

    private HttpResponse<String> request(UserSession session, String path, Map<String, Object> jsonBody,
                                         boolean withCsrf, String method, String query, Duration timeout)
            throws ApiException {
        String url = ApiHttp.uriOf(base.toString(), path) + (query == null ? "" : query);
        HttpRequest.Builder builder = HttpRequest.newBuilder(URI.create(url))
                .timeout(timeout == null ? DEFAULT_TIMEOUT : timeout)
                .header("Accept", "application/json");
        if (withCsrf) {
            // Origin deve casar com SITE_URL do servidor (sem barra final).
            builder.header("Origin", base.toString());
            builder.header("X-CSRF-Token", session.csrfToken());
        }
        if (jsonBody != null) {
            builder.header("Content-Type", "application/json");
            builder = switch (method) {
                case "POST" -> builder.POST(HttpRequest.BodyPublishers.ofString(ApiHttp.jsonOf(jsonBody)));
                default -> builder;
            };
        } else {
            builder = builder.GET();
        }
        builder.header("Authorization", "Bearer " + session.accessToken());
        try {
            return client.send(builder.build(), HttpResponse.BodyHandlers.ofString());
        } catch (IOException e) {
            throw ApiHttp.network(e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new ApiException(ApiException.STATUS_NETWORK, "interrompido", Map.of(), e);
        }
    }

    private static String stringOf(Map<?, ?> payload, String key) {
        Object value = payload.get(key);
        return value == null ? "" : String.valueOf(value);
    }
}
