package com.tlm.cli.domain.model;

/** Estado corrente do monitor, obtido de {@code GET /api/state}. */
public record LiveMonitorState(boolean connected, String username, String liveId) {

    public static LiveMonitorState offline() {
        return new LiveMonitorState(false, "", "");
    }

    public String displayName() {
        return username == null || username.isBlank() ? "" : "@" + username;
    }
}
