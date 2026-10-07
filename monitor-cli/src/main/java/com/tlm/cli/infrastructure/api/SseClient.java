package com.tlm.cli.infrastructure.api;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.ServerEvent;
import com.tlm.cli.domain.model.UserSession;
import com.tlm.cli.domain.port.EventStreamListener;
import com.tlm.cli.domain.port.EventStreamPort;
import com.tlm.cli.domain.port.StreamHandle;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Map;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Cliente SSE: {@code GET /events} com {@code Authorization: Bearer}, lê o
 * stream bloqueante em thread virtual e reporta por listener. Fechar a alça
 * encerra a leitura (desligamento local); EOF/falha despacham
 * {@code onClosed}/{@code onFailed} para o ciclo de reconexão.
 */
public final class SseClient implements EventStreamPort {

    private static final ObjectMapper MAPPER = new ObjectMapper();
    private static final TypeReference<Map<String, Object>> MAP_TYPE = new TypeReference<>() {
    };

    private final URI base;
    private final HttpClient client;

    public SseClient(String baseUrl, HttpClient client) {
        this.base = URI.create(ApiHttp.uriOf(baseUrl, ""));
        this.client = client;
    }

    @Override
    public StreamHandle open(UserSession session, EventStreamListener listener) throws ApiException {
        HttpRequest request = HttpRequest.newBuilder(URI.create(base + "/events"))
                .header("Authorization", "Bearer " + session.accessToken())
                .header("Accept", "text/event-stream")
                .GET()
                .build();
        AtomicBoolean closedByClient = new AtomicBoolean(false);
        HttpResponse<InputStream> response;
        try {
            response = client.send(request, HttpResponse.BodyHandlers.ofInputStream());
        } catch (IOException e) {
            throw ApiHttp.network(e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new ApiException(ApiException.STATUS_NETWORK, "interrompido", Map.of(), e);
        }
        if (response.statusCode() != 200) {
            String body = readSilently(response.body());
            throw ApiHttp.errorFrom(response.statusCode(), body);
        }
        InputStream body = response.body();        StreamHandle handle = () -> {
            closedByClient.set(true);
            closeSilently(body);
        };
        Thread.ofVirtual().name("sse-reader").start(() -> readStream(response, body, listener, closedByClient));
        return handle;
    }

    private void readStream(HttpResponse<InputStream> response, InputStream body,
                            EventStreamListener listener, AtomicBoolean closedByClient) {
        listener.onConnected();
        SseParser parser = new SseParser(frame ->
                listener.onEvent(new ServerEvent(frame.event(), parseJson(frame.data()), Instant.now())));
        try (body) {
            BufferedReader reader = new BufferedReader(new InputStreamReader(body, StandardCharsets.UTF_8));
            String line;
            while ((line = reader.readLine()) != null) {
                parser.acceptLine(line);
            }
            parser.close();
        } catch (IOException e) {
            if (!closedByClient.get()) {
                listener.onFailed(ApiHttp.network(e));
                return;
            }
        }
        if (!closedByClient.get()) {
            listener.onClosed();
        }
    }

    private static Map<String, Object> parseJson(String data) {
        if (data == null || data.isBlank()) {
            return Map.of();
        }
        try {
            Map<String, Object> payload = MAPPER.readValue(data, MAP_TYPE);
            return payload == null ? Map.of() : payload;
        } catch (IOException e) {
            // Tipos novos/formatação inesperada: mantém o bruto, não quebra.
            return Map.of("raw", data);
        }
    }

    private static String readSilently(InputStream body) {
        try (body) {
            byte[] chunk = body.readNBytes(4096);
            return new String(chunk, StandardCharsets.UTF_8);
        } catch (IOException ignored) {
            return "";
        }
    }

    private static void closeSilently(InputStream body) {
        try {
            body.close();
        } catch (IOException ignored) {
            // fechamento best-effort do stream
        }
    }
}
