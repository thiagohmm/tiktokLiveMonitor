package com.tlm.cli.ui;

import java.util.ArrayList;
import java.util.List;

/**
 * Resolvedor da seleção de presentes de {@code :gift <seleção>}: números
 * referem-se à última lista exibida por {@code :gift} (1-based) e nomes são
 * literais (casamento com o feed é case-insensitive no consumidor).
 */
public final class GiftSelection {

    private GiftSelection() {
    }

    /**
     * @param names presentes escolhidos na caixa original (p/ exibição)
     * @param error mensagem para o usuário quando a seleção é rejeitada
     *              (null = seleção aceita)
     */
    public record Result(List<String> names, String error) {

        public boolean ok() {
            return error == null;
        }
    }

    public static Result resolve(String selection, List<String> catalog) {
        List<String> tokens = split(selection);
        if (tokens.isEmpty()) {
            return new Result(List.of(), "nenhum presente informado; use números ou nomes da lista :gift");
        }
        List<String> names = new ArrayList<>();
        for (String token : tokens) {
            if (token.matches("\\d{1,9}")) {
                int index = Integer.parseInt(token);
                if (catalog.isEmpty()) {
                    return new Result(List.of(),
                            "catálogo vazio: rode :gift primeiro para listar os presentes");
                }
                if (index < 1 || index > catalog.size()) {
                    return new Result(List.of(),
                            "número fora da lista: " + token + " (válido: 1-" + catalog.size() + ")");
                }
                addUnique(names, catalog.get(index - 1));
            } else {
                // Nome literal: aceito como digitado (aparece no feed assim).
                addUnique(names, token);
            }
        }
        return new Result(List.copyOf(names), null);
    }

    private static void addUnique(List<String> names, String name) {
        if (!names.contains(name)) {
            names.add(name);
        }
    }

    private static List<String> split(String selection) {
        List<String> tokens = new ArrayList<>();
        for (String token : (selection == null ? "" : selection).split(",")) {
            String trimmed = token.trim();
            if (!trimmed.isEmpty()) {
                tokens.add(trimmed);
            }
        }
        return tokens;
    }
}
