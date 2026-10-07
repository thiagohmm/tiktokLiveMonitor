package com.tlm.cli.application;

/** Pausa retrátil (injetável para testes). */
@FunctionalInterface
public interface Sleeper {

    void sleep(long millis) throws InterruptedException;

    static Sleeper system() {
        return Thread::sleep;
    }
}
