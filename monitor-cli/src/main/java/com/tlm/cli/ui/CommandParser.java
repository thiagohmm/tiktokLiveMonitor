package com.tlm.cli.ui;

import java.util.Optional;

/** Parser da linha de comando da TUI (estilo vi: {@code :connect user}). */
public final class CommandParser {

    public sealed interface Command
            permits Connect, Disconnect, Gift, Pinned, Filter, Open, Close, UserFilter, Quantity,
            Clear, Help, Quit, Unknown {
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
     * Fixados: {@code argument} vazio ativa o filtro (com backfill do histórico
     * local quando o feed não tem nenhum pin); {@code "list"} imprime os
     * últimos 20 do banco.
     */
    public record Pinned(String argument) implements Command {
    }

    /** @param category alvo do filtro; {@code null} = all. */
    public record Filter(Category category) implements Command {
    }

    /**
     * Abre (ou foca) a janela do tipo. {@code argument} vazio não aplica filtro;
     * quando preenchido, é o usuário daquela janela.
     */
    public record Open(WindowKind kind, String argument) implements Command {
    }

    /** Fecha a janela em foco. */
    public record Close() implements Command {
    }

    /** Filtro de usuário da janela em foco; vazio remove o filtro. */
    public record UserFilter(String user) implements Command {
    }

    /** Quantidade mínima de presentes na janela de presentes; 0 remove o filtro. */
    public record Quantity(int minimum) implements Command {
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
            case "chat", "comentario", "comentarios", "comentário", "comentários" ->
                    new Open(WindowKind.CHAT, stripAt(argument));
            case "mensagens", "live", "feed" -> new Open(WindowKind.MESSAGES, stripAt(argument));
            case "like", "likes", "curtida", "curtidas" -> new Open(WindowKind.LIKES, stripAt(argument));
            case "follow", "follows", "seguidor", "seguidores" ->
                    new Open(WindowKind.FOLLOWS, stripAt(argument));
            case "close", "fechar" -> new Close();
            case "user", "usuario", "usuário" -> new UserFilter(stripAt(argument));
            case "qty", "qtd", "quantidade" -> parseQuantity(argument);
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

    private static Command parseQuantity(String argument) {
        String value = argument == null ? "" : argument.trim();
        try {
            int minimum = Integer.parseInt(value);
            if (minimum < 0 || minimum > 1_000_000) {
                return new Unknown("Uso: :qty <número>  (0 tira o filtro de quantidade)");
            }
            return new Quantity(minimum);
        } catch (NumberFormatException e) {
            return new Unknown("Uso: :qty <número>  (0 tira o filtro de quantidade)");
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
                Comandos (cada visão abre uma janela; [X] no mouse ou :close fecha):
                  :connect <user>       conecta direto à live (@ pode ser omitido; uma por vez)
                  :disconnect [user]    desconecta a live atual
                  :comentarios [user]   janela de comentários ao vivo (filtro opcional de usuário)
                  :gift [seleção]       janela de presentes; :gift lista o catálogo
                                        :gift 1,4 | :gift Rosa,Dino filtra essa janela
                                        :gift todos limpa o filtro de nome
                                        :gift all abre o histórico da live numa janela
                  :qty <n>              quantidade mínima na janela de presentes (0 limpa)
                  :pinned [list]        janela de fixados (busca o histórico do banco quando
                                        ainda não há nenhum ao vivo)
                  :mensagens [user]     janela com os eventos da live ao vivo
                  :curtidas :seguidores janelas desses eventos
                  :user <nome>          filtra a janela em foco por usuário (vazio limpa)
                  :close                fecha a janela em foco (Esc também, se o campo estiver vazio)
                  :filter <tipo|all>    filtra o fundo: chat/gift/like/follow/pinned/system
                  :clear                limpa o feed de fundo
                  :help                 esta ajuda
                  :quit                 encerra e desconecta a live (Ctrl+C também sai)
                Menu no topo e [X] usam o mouse. Arraste a barra para mover.
                Tab troca a janela em foco; setas e a roda rolam a janela (ou o fundo).""";
    }
}
