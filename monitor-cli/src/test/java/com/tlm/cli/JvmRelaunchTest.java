package com.tlm.cli;

import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class JvmRelaunchTest {

    @Test
    void defaultModeWarns() {
        assertFalse(JvmRelaunch.allowsMemoryApi(List.of("-Duser.dir=/tmp")));
        assertFalse(JvmRelaunch.allowsMemoryApi(List.of()));
        assertFalse(JvmRelaunch.allowsMemoryApi(null));
    }

    @Test
    void lastFlagWins() {
        assertTrue(JvmRelaunch.allowsMemoryApi(List.of(
                "--sun-misc-unsafe-memory-access=warn",
                JvmRelaunch.ALLOW)));
        assertFalse(JvmRelaunch.allowsMemoryApi(List.of(
                JvmRelaunch.ALLOW,
                "--sun-misc-unsafe-memory-access=warn")));
    }

    @Test
    void commandPutsAllowLastAndKeepsAppArgs() {
        List<String> command = JvmRelaunch.command(
                "/usr/bin/java",
                List.of("-Dfile.encoding=UTF-8"),
                "/app/monitor-cli.jar",
                "com.tlm.cli.Main",
                new String[]{"--db", "live.db"});

        assertEquals(List.of(
                "/usr/bin/java",
                "-Dfile.encoding=UTF-8",
                JvmRelaunch.ALLOW,
                "-cp",
                "/app/monitor-cli.jar",
                "com.tlm.cli.Main",
                "--db",
                "live.db"), command);
    }

    @Test
    void alreadyAllowedDoesNotRelaunch() {
        assertTrue(JvmRelaunch.command(
                "/usr/bin/java",
                List.of(JvmRelaunch.ALLOW),
                "cp",
                "com.tlm.cli.Main",
                new String[0]).isEmpty());
    }

    @Test
    void runtimeWithoutTheFlagDoesNotRelaunch() {
        assertTrue(JvmRelaunch.command(
                "/usr/bin/java",
                List.of("-Dfile.encoding=UTF-8"),
                "cp",
                "com.tlm.cli.Main",
                new String[0],
                21).isEmpty());
        assertTrue(JvmRelaunch.command(
                "/usr/bin/java",
                List.of("-Dfile.encoding=UTF-8"),
                "cp",
                "com.tlm.cli.Main",
                new String[0],
                22).isEmpty());
        assertFalse(JvmRelaunch.command(
                "/usr/bin/java",
                List.of("-Dfile.encoding=UTF-8"),
                "cp",
                "com.tlm.cli.Main",
                new String[0],
                JvmRelaunch.MIN_FEATURE_WITH_FLAG).isEmpty());
    }

    @Test
    void missingBinaryDoesNotRelaunch() {
        assertTrue(JvmRelaunch.command(
                "  ",
                List.of(),
                "cp",
                "com.tlm.cli.Main",
                new String[0]).isEmpty());
    }
}
