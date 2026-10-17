package com.tlm.cli.infrastructure.config;

import java.nio.file.Path;

/** Flags da linha de comando: {@code --db <path>} e {@code --help}. */
public record Options(Path db, boolean help) {

    /** Banco padrão: {@code ./data/tlm-local.db} (ou env {@code TLM_DB}). */
    public static Path defaultDb() {
        String env = System.getenv("TLM_DB");
        if (env != null && !env.isBlank()) {
            return Path.of(env.trim());
        }
        return Path.of("data", "tlm-local.db");
    }

    public static Options parse(String... args) {
        Path db = null;
        boolean help = false;
        for (int i = 0; i < args.length; i++) {
            String arg = args[i];
            String value = null;
            String name;
            if (arg.startsWith("--") && arg.contains("=")) {
                name = arg.substring(0, arg.indexOf('='));
                value = arg.substring(arg.indexOf('=') + 1);
            } else {
                name = arg;
            }
            switch (name) {
                case "--help", "-h" -> help = true;
                case "--db" -> db = Path.of(value != null ? value : nextValue(args, ++i));
                default -> throw new IllegalArgumentException("flag desconhecida: " + arg);
            }
        }
        return new Options(db == null ? defaultDb() : db, help);
    }

    private static String nextValue(String[] args, int index) {
        if (index >= args.length) {
            throw new IllegalArgumentException("faltou o valor após " + args[index - 1]);
        }
        String value = args[index];
        if (value.startsWith("--")) {
            throw new IllegalArgumentException("faltou o valor após " + args[index - 1]);
        }
        return value;
    }
}
