package com.tlm.cli.domain.port;

import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.LiveMonitorState;

/**
 * Destino dos eventos produzidos pela engine e pelos casos de uso. A UI
 * implementa esta porta (via hub); nenhuma implementação conhece Lanterna.
 */
public interface EventSink {

    /** Evento de domínio (chat, presente, like, seguir, fixado, sistema). */
    default void onDomainEvent(DomainEvent event) {
    }

    /** Estado do monitor mudou (header). */
    default void onStateChanged(LiveMonitorState state) {
    }

    /** Mensagem informativa (linha clara no feed). */
    default void onNotice(String message) {
    }

    /** Mensagem de erro (linha vermelha no feed). */
    default void onAlert(String message) {
    }
}
