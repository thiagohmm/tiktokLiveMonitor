package com.tlm.cli;

import com.tlm.cli.application.ConnectLiveUseCase;
import com.tlm.cli.application.DisconnectLiveUseCase;
import com.tlm.cli.application.LoginResult;
import com.tlm.cli.application.LoginUseCase;
import com.tlm.cli.application.MonitorHub;
import com.tlm.cli.application.SessionManager;
import com.tlm.cli.application.Sleeper;
import com.tlm.cli.application.WatchEventsUseCase;
import com.tlm.cli.domain.port.ConfigPort;
import com.tlm.cli.domain.port.PasswordSource;
import com.tlm.cli.infrastructure.api.ApiHttp;
import com.tlm.cli.infrastructure.api.AuthHttpApi;
import com.tlm.cli.infrastructure.api.LiveHttpApi;
import com.tlm.cli.infrastructure.api.SseClient;
import com.tlm.cli.infrastructure.config.ConfigResolver;
import com.tlm.cli.infrastructure.config.ConfigStore;
import com.tlm.cli.infrastructure.config.Options;
import com.tlm.cli.ui.TerminalUi;

import java.io.BufferedReader;
import java.io.Console;
import java.io.IOException;
import java.io.InputStreamReader;
import java.net.http.HttpClient;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Optional;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Composition root: resolve a configuração (flags &gt; env &gt; arquivo &gt;
 * prompt), faz o login inicial no console, monta os adaptadores e sobe a TUI
 * com a escuta SSE em threads virtuais.
 */
public final class Main {

    private static final int MAX_LOGIN_ATTEMPTS = 3;

    private Main() {
    }

    public static void main(String[] args) {
        Options options;
        try {
            options = Options.parse(args);
        } catch (IllegalArgumentException e) {
            System.err.println("Erro: " + e.getMessage());
            System.err.print(USAGE);
            System.exit(2);
            return;
        }
        if (options.help()) {
            System.out.print(USAGE);
            return;
        }

        try {
            run(options);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        } catch (IOException e) {
            System.err.println("Erro de terminal: " + e.getMessage());
            System.exit(1);
        }
    }

    private static void run(Options options) throws InterruptedException, IOException {
        ConfigStore store = ConfigStore.defaultFile();
        if (options.forget()) {
            boolean removed = store.clear();
            System.out.println(removed
                    ? "Config anterior esquecida (" + store.file().orElse(null) + "): e-mail/base URL serão pedidos de novo."
                    : "Não havia config anterior para esquecer.");
        }
        ConfigResolver resolver = new ConfigResolver(store, System.getenv());
        ConfigResolver.Resolved resolved = resolver.resolve(options);

        HttpClient client = ApiHttp.client();
        AuthHttpApi authApi = new AuthHttpApi(resolved.baseUrl(), client);
        LiveHttpApi liveApi = new LiveHttpApi(resolved.baseUrl(), client);

        String email = promptEmail(resolved, options);
        MutablePasswordSource passwordSource = new MutablePasswordSource();
        SessionManager sessions = new SessionManager(authApi, passwordSource);
        LoginUseCase loginUseCase = new LoginUseCase(sessions);
        char[] password = resolved.passwordRequested() ? null : resolved.envPassword().toCharArray();
        int failures = 0;
        while (true) {
            if (email.isBlank()) {
                email = promptLine("E-mail: ").trim();
                if (email.isBlank()) {
                    System.err.println("E-mail vazio.");
                    if (++failures >= MAX_LOGIN_ATTEMPTS) {
                        System.err.println("Limite de tentativas atingido; encerrando.");
                        System.exit(1);
                        return;
                    }
                    continue;
                }
            }
            if (password == null || password.length == 0) {
                password = promptPassword("Senha (não aparece; nada é gravado): ");
                if (password.length == 0) {
                    System.err.println("Senha vazia.");
                    if (++failures >= MAX_LOGIN_ATTEMPTS) {
                        System.err.println("Sem senha; encerrando.");
                        System.exit(1);
                        return;
                    }
                    continue;
                }
            }
            LoginResult result = loginUseCase.execute(email, password);
            Arrays.fill(password, '\0');
            password = null;
            if (result.success()) {
                break;
            }
            System.err.println("Falhou: " + result.message());
            if (!result.retryable()) {
                System.exit(1);
                return;
            }
            if (++failures >= MAX_LOGIN_ATTEMPTS) {
                System.err.println("Limite de tentativas atingido; encerrando.");
                System.exit(1);
                return;
            }
        }
        // Login OK: grava somente base URL + e-mail (senha NUNCA). O e-mail
        // persistido é o que de fato autenticou (pode ter sido digitado agora).
        store.save(new ConfigPort.StoredConfig(resolved.baseUrl(), email));

        ExecutorService executor = Executors.newVirtualThreadPerTaskExecutor();
        TerminalUi ui = new TerminalUi(resolved.baseUrl(), email, executor);
        passwordSource.set(ui);
        MonitorHub hub = new MonitorHub(liveApi, sessions);
        hub.addListener(ui);

        WatchEventsUseCase watch = new WatchEventsUseCase(new SseClient(resolved.baseUrl(), client),
                hub, sessions, executor, Sleeper.system());
        ConnectLiveUseCase connect = new ConnectLiveUseCase(liveApi, sessions, hub);
        DisconnectLiveUseCase disconnect = new DisconnectLiveUseCase(liveApi, sessions, hub);
        ui.bind(connect, disconnect, watch, hub);

        // Encerramento idempotente (rodado uma única vez por :quit, Ctrl+C ou
        // sinal externo). O disconnect REST vem primeiro: não depende do SSE e
        // garante que saia mesmo se ui.close() travar (custo máx. ~3s).
        AtomicBoolean shutdownDone = new AtomicBoolean(false);
        Runnable shutdownSequence = () -> {
            if (!shutdownDone.compareAndSet(false, true)) {
                return;
            }
            disconnect.executeBestEffort();
            watch.stop();
            ui.close();
            executor.shutdown();
        };
        // Thread de plataforma de propósito: virtual thread é daemon e não
        // serve como shutdown hook (a JVM pode sair antes de terminá-la).
        Thread shutdownHook = new Thread(shutdownSequence, "tlm-cli-shutdown");
        Runtime.getRuntime().addShutdownHook(shutdownHook);

        hub.refreshState();       // mútuo estado para o header
        watch.start();
        try {
            ui.run();
        } finally {
            shutdownSequence.run();
        }
    }

    /**
     * E-mail de login: prompt comum quando não resolvido; com o e-mail salvo
     * no arquivo, oferece a troca no próprio prompt (Enter mantém). Fonte
     * explícita (--email/TLM_EMAIL) ou uso não interativo (TLM_PASSWORD)
     * não pede nada — a automação segue sem toque.
     */
    private static String promptEmail(ConfigResolver.Resolved resolved, Options options) {
        String email = resolved.email();
        if (email == null || email.isBlank()) {
            return promptLine("E-mail: ").trim();
        }
        boolean explicit = (options.email() != null && !options.email().isBlank())
                || !System.getenv().getOrDefault("TLM_EMAIL", "").isBlank();
        boolean interactive = resolved.passwordRequested(); // senha virá de prompt
        if (explicit || !interactive) {
            return email;
        }
        String typed = promptLine("E-mail [" + email + "] (Enter mantém; digite outro para trocar): ").trim();
        return typed.isBlank() ? email : typed;
    }

    private static String promptLine(String prompt) {
        Console console = System.console();
        if (console != null) {
            String line = console.readLine(prompt);
            return line == null ? "" : line;
        }
        System.out.print(prompt);
        System.out.flush();
        try {
            BufferedReader reader = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8));
            String line = reader.readLine();
            return line == null ? "" : line;
        } catch (IOException e) {
            return "";
        }
    }

    private static char[] promptPassword(String prompt) {
        Console console = System.console();
        if (console != null) {
            char[] value = console.readPassword(prompt);
            return value == null ? new char[0] : value;
        }
        System.out.print(prompt + " [será digitada visivelmente]: ");
        System.out.flush();
        try {
            BufferedReader reader = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8));
            String line = reader.readLine();
            return line == null ? new char[0] : line.toCharArray();
        } catch (IOException e) {
            return new char[0];
        }
    }

    private static final String USAGE = """
            tlm-monitor — monitor de terminal para TikTok Live Monitor

            Uso: java -jar monitor-cli/target/monitor-cli.jar [opções]

            Opções:
              --url URL      base da API (padrão: https://livemonitortk.com.br)
              --email EMAIL  e-mail de acesso (sobrescreve e regrava o salvo)
              --esquecer     apaga a config salva (e-mail/base URL) e pede tudo de novo
              --help         esta ajuda

            Fluxo: login no console (a senha nunca é gravada), depois a TUI:
              header   status da sessão SSE e da live atual + tempo no ar
              feed     eventos em tempo real com cores por tipo (PageUp/PageDown rolam)
              rodapé   contadores de comentários/presentes/curtidas/seguidores/fixados
              comandos :connect <user>     | :disconnect [user]
                       :gift [seleção]     (lista o catálogo; :gift 1,4 ou :gift Rosa,Dino filtra)
                       :pinned [list]      (fixados ao vivo; :pinned list traz os últimos 20)
                       :filter <all|chat|gift|like|follow|pinned|system> | :clear | :help
                       :quit (Ctrl+C também encerra; sair desconecta a live, best-effort)

            Trocar o login: no prompt "E-mail [salvo]" digite outro e-mail ou use
            --email/TLM_EMAIL; para apagar o e-mail salvo, rode com --esquecer.
            Ambiente: TLM_EMAIL, TLM_PASSWORD (sobrescrevem o arquivo; a senha não é persistida).
            Config:   ~/.config/tlm-cli/config.properties (base URL + e-mail, permissão 600).
            """;

    /** PasswordSource substituível: console antes da TUI, UI depois. */
    private static final class MutablePasswordSource implements PasswordSource {

        private volatile PasswordSource delegate = info -> Optional.empty();

        void set(PasswordSource delegate) {
            this.delegate = delegate;
        }

        @Override
        public Optional<char[]> promptPassword(String info) {
            return delegate.promptPassword(info);
        }
    }
}
