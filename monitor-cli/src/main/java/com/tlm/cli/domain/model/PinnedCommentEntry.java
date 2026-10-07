package com.tlm.cli.domain.model;

/**
 * Item do histórico REST de comentários fixados
 * ({@code GET /api/pinned-comments}); {@code timestamp} chega como string
 * ISO (RFC3339) e é exibido best-effort.
 */
public record PinnedCommentEntry(String liveName, String uniqueId, String nickname,
                                 String comment, String timestamp) {
}
