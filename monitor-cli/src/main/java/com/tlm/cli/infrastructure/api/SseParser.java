package com.tlm.cli.infrastructure.api;

import java.util.function.Consumer;

/**
 * Parser SSE então (sem dependências): aceita linhas/chunks parciais, ignora
 * comentários ({@code : ping}) e tolera eventos sem nome, campos extra
 * ({@code id:}, {@code retry:}) e CRLF. Formato do servidor: exatamente
 * {@code event: <tipo>\ndata: <json de uma linha>\n\n}.
 */
public final class SseParser {

    /** Frame completo (dados multi-linha mantêm os {@code \n} internos). */
    public record Frame(String event, String data) {
    }

    private final Consumer<Frame> sink;
    private final StringBuilder lineBuffer = new StringBuilder();
    private final StringBuilder dataBuffer = new StringBuilder();
    private String eventName;

    public SseParser(Consumer<Frame> sink) {
        this.sink = sink;
    }

    /** Alimenta com um pedaço do stream (linha inteira ou fragmento). */
    public void accept(CharSequence chunk) {
        for (int i = 0; i < chunk.length(); i++) {
            char c = chunk.charAt(i);
            if (c == '\n') {
                handleLine(lineBuffer.toString());
                lineBuffer.setLength(0);
            } else if (c != '\r') {
                lineBuffer.append(c);
            }
        }
    }

    /** Linha completa já separada (atalho para BufferedReaders). */
    public void acceptLine(String line) {
        handleLine(stripCr(line));
    }

    /** Fim do stream: despacha frame parcial pendente, se houver. */
    public void close() {
        if (lineBuffer.length() > 0) {
            handleLine(lineBuffer.toString());
            lineBuffer.setLength(0);
        }
        dispatch();
    }

    private void handleLine(String raw) {
        String line = stripCr(raw);
        if (line.startsWith(":")) {
            return; // keepalive/comentário
        }
        if (line.isBlank()) {
            dispatch();
            return;
        }
        if (line.startsWith("event:")) {
            eventName = valueAfter(line, "event:".length());
        } else if (line.startsWith("data:")) {
            String value = valueAfter(line, "data:".length());
            if (dataBuffer.length() > 0) {
                dataBuffer.append('\n');
            }
            dataBuffer.append(value);
        }
        // Campos desconhecidos (id:, retry:, etc.) são ignorados por design.
    }

    private void dispatch() {
        String data = dataBuffer.toString();
        dataBuffer.setLength(0);
        String event = eventName;
        eventName = null;
        boolean hasData = !data.isBlank();
        boolean hasEvent = event != null && !event.isBlank();
        if (!hasData && !hasEvent) {
            return; // frames vazios não despacham
        }
        sink.accept(new Frame(hasEvent ? event : "message", data));
    }

    private static String valueAfter(String line, int offset) {
        String value = line.substring(offset);
        if (value.startsWith(" ")) {
            value = value.substring(1);
        }
        return value;
    }

    private static String stripCr(String line) {
        return line.endsWith("\r") ? line.substring(0, line.length() - 1) : line;
    }
}
