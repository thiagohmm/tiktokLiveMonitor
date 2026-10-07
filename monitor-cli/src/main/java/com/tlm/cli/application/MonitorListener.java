package com.tlm.cli.application;

/**
 * Ponte entre casos de uso e a UI: nenhum caso de uso conhece Lanterna; eles
 * publicam por aqui e o listener (implementado pela UI) desenha e registra
 * contadores.
 */
public interface MonitorListener {

    /** Evento de domínio decodificado (chat, presente, like, seguir, sistema). */
    default void onDomainEvent(com.tlm.cli.domain.model.DomainEvent event) {
    }

    /** Estado do monitor mudou (header). */
    default void onStateChanged(com.tlm.cli.domain.model.LiveMonitorState state) {
    }

    /** Stream (re)estabelecido. */
    default void onStreamUp() {
    }

    /** Stream encerrado pelo servidor ou por erro de rede. */
    default void onStreamDown(String reason) {
    }

    /** Mensagem informativa (linha de sistema no feed). */
    default void onNotice(String message) {
    }

    /** Mensagem de erro (linha de erro no feed). */
    default void onAlert(String message) {
    }
}
