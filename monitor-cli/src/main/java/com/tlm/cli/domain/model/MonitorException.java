package com.tlm.cli.domain.model;

/** Falha de operação local (banco, engine, uso inválido de comando). */
public class MonitorException extends Exception {

    public MonitorException(String message) {
        super(message);
    }

    public MonitorException(String message, Throwable cause) {
        super(message, cause);
    }
}
