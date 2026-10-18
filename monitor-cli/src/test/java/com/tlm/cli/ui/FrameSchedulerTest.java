package com.tlm.cli.ui;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class FrameSchedulerTest {

    @Test
    void drawsWhenSomethingChanged() {
        FrameScheduler scheduler = new FrameScheduler();
        assertTrue(scheduler.shouldDraw(true, false, false, 0));
        assertTrue(scheduler.shouldDraw(false, true, false, 0));
        assertTrue(scheduler.shouldDraw(false, false, true, 0));
    }

    @Test
    void skipsIdleFramesWithinTheSameSecond() {
        FrameScheduler scheduler = new FrameScheduler();
        assertTrue(scheduler.shouldDraw(false, false, false, 0));
        assertFalse(scheduler.shouldDraw(false, false, false, 0));
        assertFalse(scheduler.shouldDraw(false, false, false, 0));
    }

    @Test
    void uptimeSecondChangeForcesRepaint() {
        FrameScheduler scheduler = new FrameScheduler();
        assertTrue(scheduler.shouldDraw(false, false, false, 3));
        assertFalse(scheduler.shouldDraw(false, false, false, 3));
        assertTrue(scheduler.shouldDraw(false, false, false, 4));
        assertFalse(scheduler.shouldDraw(false, false, false, 4));
    }
}
