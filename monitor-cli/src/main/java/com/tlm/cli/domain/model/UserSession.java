package com.tlm.cli.domain.model;

/**
 * Sessão autenticada em memória. O token de acesso e o token CSRF nunca
 * são persistidos em arquivo ou log.
 */
public record UserSession(String accessToken, String csrfToken) {

    public UserSession {
        if (accessToken == null || accessToken.isBlank()) {
            throw new IllegalArgumentException("accessToken obrigatório");
        }
        csrfToken = csrfToken == null ? "" : csrfToken;
    }

    public UserSession withCsrfToken(String novoCsrfToken) {
        return new UserSession(accessToken, novoCsrfToken);
    }
}
