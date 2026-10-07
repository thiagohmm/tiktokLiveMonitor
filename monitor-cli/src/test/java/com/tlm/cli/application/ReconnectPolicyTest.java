package com.tlm.cli.application;

import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;

class ReconnectPolicyTest {

    @Test
    void backoffSequenceIsOneTwoFourEightThenCapped() {
        ReconnectPolicy policy = new ReconnectPolicy();
        List<Long> delays = new ArrayList<>();
        for (int i = 0; i < 6; i++) {
            delays.add(policy.nextDelayMillis());
        }
        assertEquals(List.of(1000L, 2000L, 4000L, 8000L, 15000L, 15000L), delays);
    }

    @Test
    void resetRestartsSequence() {
        ReconnectPolicy policy = new ReconnectPolicy();
        policy.nextDelayMillis();
        policy.nextDelayMillis();
        policy.nextDelayMillis();
        policy.reset();
        assertEquals(1000L, policy.nextDelayMillis());
    }

    @Test
    void attemptsCounterFollowsSequence() {
        ReconnectPolicy policy = new ReconnectPolicy();
        policy.nextDelayMillis();
        policy.nextDelayMillis();
        assertEquals(2, policy.attempts());
        policy.reset();
        assertEquals(0, policy.attempts());
    }
}
