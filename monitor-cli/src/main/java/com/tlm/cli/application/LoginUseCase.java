package com.tlm.cli.application;

import com.tlm.cli.domain.model.ApiException;

/** Caso de uso de login (usado pelo prompt inicial e pela recuperação pós-401). */
public final class LoginUseCase {

    private final SessionManager sessions;

    public LoginUseCase(SessionManager sessions) {
        this.sessions = sessions;
    }

    public LoginResult execute(String email, char[] password) {
        try {
            sessions.login(email, password);
            return LoginResult.ok();
        } catch (ApiException error) {
            return LoginResult.from(error);
        }
    }
}
