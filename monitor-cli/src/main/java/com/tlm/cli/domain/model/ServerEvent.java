package com.tlm.cli.domain.model;

import java.time.Instant;
import java.util.Map;
import java.util.Optional;

/**
 * Evento recebido pelo stream SSE. O payload é aberto (mapa), espelhando o
 * backend ({@code EventData = map[string]interface{}}): tipos novos do
 * servidor devem ser tolerados sem quebrar o parser.
 */
public record ServerEvent(String type, Map<String, Object> data, Instant receivedAt) {

    public ServerEvent {
        data = data == null ? Map.of() : data;
        receivedAt = receivedAt == null ? Instant.now() : receivedAt;
    }

    public Optional<Object> value(String key) {
        return Optional.ofNullable(data.get(key));
    }

    public Optional<String> string(String key) {
        Object v = data.get(key);
        return v == null ? Optional.empty() : Optional.of(String.valueOf(v));
    }
}
