package com.tlm.cli.infrastructure.api;

import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class SseParserTest {

    private final List<SseParser.Frame> frames = new ArrayList<>();

    private SseParser parser() {
        return new SseParser(frames::add);
    }

    @Test
    void parsesFrameInOneChunk() {
        SseParser parser = parser();
        parser.accept("event: server-state\ndata: {\"connected\":false}\n\n");
        parser.close();
        assertEquals(1, frames.size());
        assertEquals("server-state", frames.get(0).event());
        assertEquals("{\"connected\":false}", frames.get(0).data());
    }

    @Test
    void parsesAcrossPartialChunks() {
        SseParser parser = parser();
        parser.accept("eve");
        parser.accept("nt: new-chat-message\n");
        parser.accept("data: {\"uniq");
        parser.accept("ueId\":\"u1\"}");
        parser.accept("\n\n");
        parser.close();
        assertEquals(1, frames.size());
        assertEquals("new-chat-message", frames.get(0).event());
        assertEquals("{\"uniqueId\":\"u1\"}", frames.get(0).data());
    }

    @Test
    void ignoresKeepaliveComment() {
        SseParser parser = parser();
        parser.accept(": ping\n\n");
        parser.close();
        assertTrue(frames.isEmpty());
    }

    @Test
    void eventWithoutNameUsesMessageDefault() {
        SseParser parser = parser();
        parser.accept("data: {\"raw\":true}\n\n");
        parser.close();
        assertEquals(1, frames.size());
        assertEquals("message", frames.get(0).event());
        assertEquals("{\"raw\":true}", frames.get(0).data());
    }

    @Test
    void multiLineDataIsJoinedWithNewlines() {
        SseParser parser = parser();
        parser.accept("event: t\ndata: linha1\ndata: linha2\n\n");
        parser.close();
        assertEquals("linha1\nlinha2", frames.get(0).data());
    }

    @Test
    void multipleFramesInOneChunk() {
        SseParser parser = parser();
        parser.accept("event: a\ndata: 1\n\nevent: b\ndata: 2\n\n");
        parser.close();
        assertEquals(2, frames.size());
        assertEquals("a", frames.get(0).event());
        assertEquals("b", frames.get(1).event());
    }

    @Test
    void toleratesCrlfAndExtraFields() {
        SseParser parser = parser();
        parser.accept("id: 42\r\nevent: t\r\ndata: x\r\nretry: 300\r\n\r\n");
        parser.close();
        assertEquals(1, frames.size());
        assertEquals("t", frames.get(0).event());
        assertEquals("x", frames.get(0).data());
    }

    @Test
    void acceptLineShortcutMatches() {
        SseParser parser = parser();
        parser.acceptLine("event: server-state");
        parser.acceptLine("data: {\"connected\":true}");
        parser.acceptLine("");
        parser.close();
        assertEquals(1, frames.size());
        assertEquals("{\"connected\":true}", frames.get(0).data());
    }

    @Test
    void eventOnlyFrameIsDispatchedTolerantly() {
        SseParser parser = parser();
        parser.accept("event: mystery\n\n");
        parser.close();
        assertEquals(1, frames.size());
        assertEquals("mystery", frames.get(0).event());
        assertEquals("", frames.get(0).data());
    }

    @Test
    void blankLinesWithoutContentAreNotFrames() {
        SseParser parser = parser();
        parser.accept("\n\n\n\n");
        parser.close();
        assertTrue(frames.isEmpty());
    }

    @Test
    void unnamedEventIsNormalizedToMessageInSink() {
        List<Object> sink = new ArrayList<>();
        new SseParser(sink::add).accept("data: x\n\n");
        assertEquals(1, sink.size());
        SseParser.Frame frame = (SseParser.Frame) sink.get(0);
        assertEquals("message", frame.event());
    }
}
