package com.tlm.cli.ui;

/**
 * Decide se o quadro precisa ser redesenhado. O loop de entrada continua
 * rodando a cada tique; só o desenho/refresh é economizado quando nada mudou.
 * A troca do segundo do uptime força um repaint por segundo (relógio do
 * cabeçalho) e serve de rede de segurança para mutações não marcadas.
 */
final class FrameScheduler {

    private static final long NEVER = Long.MIN_VALUE;

    private long lastSecond = NEVER;

    boolean shouldDraw(boolean pendingRan, boolean inputHandled, boolean resized, long uptimeSecond) {
        if (pendingRan || inputHandled || resized || uptimeSecond != lastSecond) {
            lastSecond = uptimeSecond;
            return true;
        }
        return false;
    }
}
