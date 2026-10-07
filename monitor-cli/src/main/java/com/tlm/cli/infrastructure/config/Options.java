package com.tlm.cli.infrastructure.config;

/** Flags da linha de comando (--url, --email, --esquecer, --help). */
public record Options(String url, String email, boolean help, boolean forget) {

    public static Options parse(String... args) {
        String url = null;
        String email = null;
        boolean help = false;
        boolean forget = false;
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
                case "--url" -> url = value != null ? value : nextValue(args, ++i);
                case "--email" -> email = value != null ? value : nextValue(args, ++i);
                case "--esquecer", "--forget" -> forget = true;
                default -> throw new IllegalArgumentException("flag desconhecida: " + arg);
            }
        }
        return new Options(url, email, help, forget);
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
