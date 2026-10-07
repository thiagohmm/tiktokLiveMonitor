package com.tlm.cli.ui;

import java.util.Optional;

/** Parser da linha de comando da TUI (estilo vi: {@code :connect user}). */
public final class CommandParser {

    public sealed interface Command
            permits Connect, Disconnect, Gift, Pinned, Filter, Clear, Help, Quit, Unknown {
    }

    /** @param username username do TikTok (sem '@'). */
    public record Connect(String username) implements Command {
    }

    /** @param username vazio/nulo desconecta o monitor corrente. */
    public record Disconnect(String username) implements Command {
    }

    /**
     * Filtro por presente: {@code argument} vazio lista o catálogo;
     * {@code "all"} limpa a seleção; caso contrário é a seleção
     * (números da última lista e/ou nomes, separados por vírgula).
     */
    public record Gift(String argument) implements Command {
    }

    /**
     * Fixados: {@code argument} vazio ativa o acompanhamento ao vivo;
     * {@code "list"} imprime o histórico dos últimos 20.
     */
    public record Pinned(String argument) implements Command {
    }

    /** @param category alvo do filtro; {@code null} = all. */
    public record Filter(Category category) implements Command {
    }

    public record Clear() implements Command {
    }

    public record Help() implements Command {
    }

    public record Quit() implements Command {
    }

    /** Entrada inválida ou texto solto; {@code message} explica o porquê. */
    public record Unknown(String message) implements Command {
    }

    private CommandParser() {
    }

    /**
     * @return comando a executar ou vazio quando a linha está em branco.
     */
    public static Optional<Command> parse(String raw) {
        String line = raw == null ? "" : raw.trim();
        if (line.isEmpty()) {
            return Optional.empty();
        }
        if (!line.startsWith(":")) {
            return Optional.of(new Unknown("Use um comando de ':' (veja :help)."));
        }
        String body = line.substring(1).trim();
        if (body.isEmpty()) {
            return Optional.of(new Help());
        }
        String[] parts = body.split("\\s+", 2);
        String command = parts[0].toLowerCase();
        String argument = parts.length > 1 ? parts[1].trim() : "";
        return Optional.of(dispatch(command, argument));
    }

    private static Command dispatch(String command, String argument) {
        return switch (command) {
            case "connect" -> parseConnect(argument);
            case "disconnect" -> new Disconnect(stripAt(argument));
            case "gift", "gifts", "presente", "presentes" -> new Gift(argument);
            case "pinned", "pin", "fixado", "fixados" -> parsePinned(argument);
            case "filter" -> parseFilter(argument);
            case "clear" -> new Clear();
            case "help", "h", "?" -> new Help();
            case "quit", "q" -> new Quit();
            default -> new Unknown("Comando desconhecido '" + command + "'. Veja :help.");
        };
    }

    private static Command parseConnect(String argument) {
        String username = stripAt(argument);
        if (username.isBlank()) {
            return new Unknown("Uso: :connect <username>");
        }
        return new Connect(username);
    }

    private static Command parseFilter(String argument) {
        try {
            return new Filter(Category.parseFilter(argument));
        } catch (IllegalArgumentException e) {
            return new Unknown("Uso: :filter <all|chat|gift|like|follow|pinned|system>");
        }
    }

    private static Command parsePinned(String argument) {
        String value = argument.trim().toLowerCase();
        if (value.isEmpty()) {
            return new Pinned("");
        }
        if (value.equals("list") || value.equals("lista")) {
            return new Pinned("list");
        }
        return new Unknown("Uso: :pinned [list]");
    }

    private static String stripAt(String value) {
        String trimmed = value.trim();
        return trimmed.startsWith("@") ? trimmed.substring(1) : trimmed;
    }

    public static String helpText() {
        return """
                Comandos:
                  :connect <user>       conecta a uma live (@ pode ser omitido)
                  :disconnect [user]    desconecta o monitor (corrente, se omitido)
                  :gift [seleção]       lista o catálogo ou filtra pelos presentes escolhidos
                                        (ex.: :gift 1,4 | :gift Rosa,Dino | :gift all volta a tudo)
                  :filter <tipo|all>    mostra só chat/gift/like/follow/pinned/system
                  :pinned [list]        fixados ao vivo; :pinned list imprime os últimos 20
                  :clear                limpa o feed
                  :help                 esta ajuda
                  :quit                 encerra e desconecta a live (Ctrl+C também sai)
                PageUp/PageDown rolam o feed; Esc limpa o campo de digitação.""";
    }
}
