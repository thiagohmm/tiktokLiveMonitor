package com.tlm.cli.infrastructure.api;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.tlm.cli.domain.model.ApiException;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.time.Duration;
import java.util.List;
import java.util.Map;

/** Utilitários comuns dos adaptadores HTTP (Jackson, erros, client). */
public final class ApiHttp {

    private static final ObjectMapper MAPPER = new ObjectMapper();

    private ApiHttp() {
    }

    public static HttpClient client() {
        return HttpClient.newBuilder()
                .followRedirects(HttpClient.Redirect.NEVER)
                .connectTimeout(Duration.ofSeconds(10))
                .build();
    }

    static String jsonOf(Map<String, ?> body) {
        try {
            return MAPPER.writeValueAsString(body);
        } catch (IOException e) {
            throw new IllegalStateException("falha ao serializar corpo JSON", e);
        }
    }

    static Map<String, Object> parseObject(String raw) {
        if (raw == null || raw.isBlank()) {
            return Map.of();
        }
        try {
            Map<String, Object> parsed = MAPPER.readValue(raw, new TypeReference<Map<String, Object>>() {
            });
            return parsed == null ? Map.of() : parsed;
        } catch (IOException e) {
            return Map.of();
        }
    }

    /** Parse tolerante de respostas em array JSON; branco/malformado/não-array → lista vazia. */
    static List<Object> parseArray(String raw) {
        if (raw == null || raw.isBlank()) {
            return List.of();
        }
        try {
            List<Object> parsed = MAPPER.readValue(raw, new TypeReference<List<Object>>() {
            });
            return parsed == null ? List.of() : parsed;
        } catch (IOException e) {
            return List.of();
        }
    }

    /** Corpo de erro do backend: {@code {error|message, code, ...}}; corpos em texto puro ficam como estão. */
    static String errorMessage(int status, String body) {
        Map<String, Object> payload = parseObject(body);
        Object error = payload.get("error");
        if (error instanceof String s && !s.isBlank()) {
            return s;
        }
        Object message = payload.get("message");
        if (message instanceof String s && !s.isBlank()) {
            return s;
        }
        if (body != null && !body.isBlank()) {
            String plain = body.trim();
            return plain.length() > 200 ? plain.substring(0, 200) : plain;
        }
        return "HTTP " + status;
    }

    static ApiException errorFrom(int status, String body) {
        return new ApiException(status, errorMessage(status, body), parseObject(body));
    }

    static ApiException network(IOException cause) {
        return new ApiException(ApiException.STATUS_NETWORK, describe(cause), Map.of(), cause);
    }

    static String uriOf(String baseUrl, String path) {
        String base = baseUrl == null ? "" : baseUrl.trim();
        while (base.endsWith("/")) {
            base = base.substring(0, base.length() - 1);
        }
        return base + path;
    }

    private static String describe(IOException cause) {
        String message = cause.getMessage() == null ? cause.getClass().getSimpleName() : cause.getMessage();
        return message.isBlank() ? cause.getClass().getSimpleName() : message;
    }
}
