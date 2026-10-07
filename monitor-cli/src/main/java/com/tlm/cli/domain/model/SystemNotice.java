package com.tlm.cli.domain.model;

import java.time.Instant;

/**
 * Evento de sistema/controle (estado do servidor, conexão, tipos
 * desconhecidos renderizados com payload bruto).
 */
public record SystemNotice(Instant at, String message, String eventType, String rawPayload) implements DomainEvent {

    public static SystemNotice of(Instant at, String message) {
        return new SystemNotice(at, message, "", "");
    }
}
