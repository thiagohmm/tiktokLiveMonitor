package com.tlm.cli.application;

import com.tlm.cli.domain.model.ApiException;

/** Mensagens de erro de API em PT-BR para a UI. */
public final class ApiErrors {

    private ApiErrors() {
    }

    public static String describe(ApiException error) {
        String detail = error.getMessage() == null ? "erro desconhecido" : error.getMessage();
        if (error.status() == ApiException.STATUS_NETWORK) {
            return "Falha de rede: " + detail;
        }
        return switch (error.status()) {
            case 401 -> "Não autorizado: " + detail;
            case 403 -> "Acesso negado: " + detail;
            case 404 -> "Recurso não encontrado: " + detail;
            case 409 -> "Conflito: " + detail;
            case 429 -> {
                long wait = error.retryAfterSec().map(Number::longValue).orElse(60L);
                yield "Bloqueado por excesso de tentativas; aguarde " + wait + "s";
            }
            case 502 -> "Servidor de autenticação indisponível (502)";
            case 503 -> "Servidor com muitos clientes conectados; tente novamente em instantes";
            default -> "Erro HTTP " + error.status() + ": " + detail;
        };
    }
}
