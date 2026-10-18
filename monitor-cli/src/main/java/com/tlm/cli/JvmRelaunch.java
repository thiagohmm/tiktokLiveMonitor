package com.tlm.cli;

import java.io.IOException;
import java.lang.management.ManagementFactory;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;

/**
 * O protobuf (usado ao conectar na live) chama {@code sun.misc.Unsafe}. No
 * Java 25 o padrão é avisar no stderr nativo — isso rabisca a tela da
 * Lanterna e o refresh em delta não redesenha as janelas. A flag só vale na
 * linha de comando, então este processo entrega o lugar a um JVM novo com
 * {@code --sun-misc-unsafe-memory-access=allow}.
 */
final class JvmRelaunch {

    static final String ALLOW = "--sun-misc-unsafe-memory-access=allow";

    /** A flag só existe a partir do JDK 23; em 21/22 relançar quebraria o boot. */
    static final int MIN_FEATURE_WITH_FLAG = 23;

    private JvmRelaunch() {
    }

    /** @return {@code true} quando este processo só esperou o JVM filho */
    static boolean handOffIfNeeded(String[] appArgs) {
        List<String> vmArgs = ManagementFactory.getRuntimeMXBean().getInputArguments();
        Optional<String> javaBin = ProcessHandle.current().info().command().filter(bin -> !bin.isBlank());
        if (javaBin.isEmpty()) {
            return false;
        }
        List<String> command = command(
                javaBin.get(),
                vmArgs,
                System.getProperty("java.class.path"),
                Main.class.getName(),
                appArgs);
        if (command.isEmpty()) {
            return false;
        }
        ignoreStopSignals();
        try {
            int code = new ProcessBuilder(command).inheritIO().start().waitFor();
            System.exit(code);
        } catch (IOException e) {
            return false;
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            System.exit(1);
        }
        return true;
    }

    /**
     * Linha do JVM filho. Vazia quando o acesso já está permitido (a última
     * ocorrência da flag ganha), quando o runtime não conhece a flag (JDK 21/22)
     * ou quando não há binário para relançar.
     */
    static List<String> command(String javaBin, List<String> vmArgs, String classpath,
                                String mainClass, String[] appArgs) {
        return command(javaBin, vmArgs, classpath, mainClass, appArgs, Runtime.version().feature());
    }

    static List<String> command(String javaBin, List<String> vmArgs, String classpath,
                                String mainClass, String[] appArgs, int feature) {
        if (javaBin == null || javaBin.isBlank() || allowsMemoryApi(vmArgs)
                || feature < MIN_FEATURE_WITH_FLAG) {
            return List.of();
        }
        List<String> command = new ArrayList<>();
        command.add(javaBin);
        if (vmArgs != null) {
            command.addAll(vmArgs);
        }
        command.add(ALLOW);
        command.add("-cp");
        command.add(classpath == null ? "" : classpath);
        command.add(mainClass);
        if (appArgs != null) {
            command.addAll(List.of(appArgs));
        }
        return List.copyOf(command);
    }

    static boolean allowsMemoryApi(List<String> vmArgs) {
        String mode = "warn";
        if (vmArgs != null) {
            for (String arg : vmArgs) {
                String prefix = "--sun-misc-unsafe-memory-access=";
                if (arg != null && arg.startsWith(prefix)) {
                    mode = arg.substring(prefix.length());
                }
            }
        }
        return "allow".equals(mode);
    }

    /**
     * O filho fica no mesmo grupo de processos. Se o pai morrer no SIGINT, o
     * shell volta ao prompt com a TUI ainda segurando o terminal.
     */
    private static void ignoreStopSignals() {
        try {
            Class<?> signal = Class.forName("sun.misc.Signal");
            Class<?> handler = Class.forName("sun.misc.SignalHandler");
            Object ignore = Proxy.newProxyInstance(
                    handler.getClassLoader(),
                    new Class<?>[]{handler},
                    (proxy, method, args) -> {
                        Class<?> type = method.getReturnType();
                        if (type == boolean.class) {
                            return false;
                        }
                        if (type == int.class) {
                            return 0;
                        }
                        return null;
                    });
            var constructor = signal.getConstructor(String.class);
            var handle = signal.getMethod("handle", signal, handler);
            for (String name : List.of("INT", "TERM", "HUP")) {
                handle.invoke(null, constructor.newInstance(name), ignore);
            }
        } catch (ReflectiveOperationException ignored) {
            // Sem o ignore, um sinal externo pode devolver o prompt cedo demais.
        }
    }
}
