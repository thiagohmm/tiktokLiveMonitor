package com.tlm.cli.application;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.UserSession;
import com.tlm.cli.domain.port.AuthPort;
import com.tlm.cli.domain.port.PasswordSource;

import java.util.Arrays;
import java.util.Map;
import java.util.Optional;

/**
 * Guardião da sessão em memória: login, renovação de CSRF (com 1 tentativa
 * extra nos comandos) e recuperação pós-401 (1 prompt de senha).
 */
public final class SessionManager {

    /** Operação autenticada que pode exigir nova tentativa após renovar CSRF. */
    @FunctionalInterface
    public interface CsrfOperation<T> {
        T apply(UserSession session) throws ApiException;
    }

    private final AuthPort auth;
    private final PasswordSource passwords;
    private volatile UserSession session;
    private volatile String email = "";

    public SessionManager(AuthPort auth, PasswordSource passwords) {
        this.auth = auth;
        this.passwords = passwords;
    }

    public void setEmail(String email) {
        this.email = email == null ? "" : email.trim();
    }

    public String currentUserEmail() {
        return email;
    }

    public Optional<UserSession> current() {
        return Optional.ofNullable(session);
    }

    public boolean hasSession() {
        return session != null;
    }

    public void login(String loginEmail, char[] password) throws ApiException {
        UserSession fresh = auth.login(loginEmail, password);
        this.email = loginEmail == null ? "" : loginEmail.trim();
        this.session = fresh;
    }

    /** Executa a operação; em 403 de CSRF renova o token via /me e tenta 1×. */
    public <T> T send(CsrfOperation<T> operation) throws ApiException {
        UserSession current = session;
        if (current == null) {
            throw new ApiException(401, "Sessão não iniciada", Map.of());
        }
        try {
            return operation.apply(current);
        } catch (ApiException error) {
            if (!error.isCsrfError()) {
                throw error;
            }
            UserSession refreshed = auth.me(current);
            this.session = refreshed;
            return operation.apply(refreshed);
        }
    }

    /**
     * Recuperação após 401: pede a senha pela UI (1 tentativa) e refaz o
     * login. Retorna false quando o usuário cancela ou o login falha.
     */
    public boolean recoverAfter401() {
        UserSession current = session;
        if (current != null) {
            try {
                this.session = auth.me(current);
                return true;
            } catch (ApiException ignored) {
                // Sessão realmente revogada: segue para o prompt de senha.
            }
        }
        String targetEmail = email.isBlank() ? "sua conta" : email;
        Optional<char[]> password = passwords.promptPassword("Sessão expirada — digite a senha de " + targetEmail + " (ESC cancela)");
        if (password.isEmpty()) {
            return false;
        }
        char[] passwordValue = password.get();
        try {
            login(targetEmail, passwordValue);
            return true;
        } catch (ApiException error) {
            return false;
        } finally {
            Arrays.fill(passwordValue, '\0');
        }
    }
}
