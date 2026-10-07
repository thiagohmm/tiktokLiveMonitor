package com.tlm.cli.application;

import com.tlm.cli.domain.model.ApiException;

/** Resultado do login para a UI apresentar (e para decidir re-prompt). */
public record LoginResult(boolean success, String message, Integer retryAfterSec, boolean retryable) {

    public static LoginResult ok() {
        return new LoginResult(true, "Autenticado", null, false);
    }

    /** Traduz a falha do backend em mensagem apresentável + política de retry. */
    public static LoginResult from(ApiException error) {
        if (error.isNeedsActivation()) {
            String link = error.redirectTo().map(url -> " Abra: " + url).orElse("");
            return new LoginResult(false, "Conta migrada: defina uma nova senha para continuar." + link, null, false);
        }
        if (error.isLockout()) {
            Integer wait = error.retryAfterSec().map(Number::intValue).orElse(60);
            return new LoginResult(false, "Bloqueado por excesso de tentativas. Tente novamente em " + wait + "s.", wait, false);
        }
        String detail = error.getMessage() == null ? "erro desconhecido" : error.getMessage();
        return switch (error.status()) {
            case 401 -> {
                String remaining = error.remainingAttempts().map(n -> " (" + n.intValue() + " tentativas restantes)").orElse("");
                yield new LoginResult(false, "Credenciais inválidas" + remaining + ".", null, true);
            }
            case 403 -> new LoginResult(false, detail, null, false);
            case 502 -> new LoginResult(false, "Servidor de autenticação indisponível (502). Tente novamente.", null, true);
            case ApiException.STATUS_NETWORK -> new LoginResult(false, "Falha de rede ao contatar o servidor: " + detail, null, true);
            default -> new LoginResult(false, "Erro HTTP " + error.status() + ": " + detail, null, false);
        };
    }

    public boolean locked() {
        return retryAfterSec != null;
    }
}
