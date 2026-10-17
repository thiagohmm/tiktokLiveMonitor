package com.tlm.cli.domain.model;

/**
 * Presente já gravado no SQLite local. {@code timestamp} é ISO-8601 UTC.
 */
public record GiftEntry(String nickname, String giftName, int count, String timestamp) {
}
