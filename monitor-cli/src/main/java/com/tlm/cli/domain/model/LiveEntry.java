package com.tlm.cli.domain.model;

/** Uma live monitorada pela organização, vindas de {@code GET /api/lives}. */
public record LiveEntry(String live, String state) {
}
