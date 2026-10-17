package com.tlm.cli.ui;

import com.googlecode.lanterna.TerminalPosition;
import com.googlecode.lanterna.TerminalSize;
import com.googlecode.lanterna.TextColor;
import com.googlecode.lanterna.graphics.TextGraphics;
import com.googlecode.lanterna.input.KeyStroke;
import com.googlecode.lanterna.input.KeyType;
import com.googlecode.lanterna.input.MouseAction;
import com.googlecode.lanterna.screen.Screen;
import com.googlecode.lanterna.terminal.DefaultTerminalFactory;
import com.googlecode.lanterna.terminal.MouseCaptureMode;
import com.tlm.cli.application.ConnectLiveUseCase;
import com.tlm.cli.application.DisconnectLiveUseCase;
import com.tlm.cli.application.MonitorHub;
import com.tlm.cli.domain.model.ChatMessage;
import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.FollowEvent;
import com.tlm.cli.domain.model.GiftEntry;
import com.tlm.cli.domain.model.GiftEvent;
import com.tlm.cli.domain.model.LikeEvent;
import com.tlm.cli.domain.model.LiveMonitorState;
import com.tlm.cli.domain.model.MonitorException;
import com.tlm.cli.domain.model.PinnedCommentEntry;
import com.tlm.cli.domain.model.PinnedCommentEvent;
import com.tlm.cli.domain.model.SystemNotice;
import com.tlm.cli.domain.port.EventSink;

import java.io.IOException;
import java.io.OutputStream;
import java.io.PrintStream;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneId;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.Collections;
import java.util.EnumMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Queue;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.ExecutorService;
import java.util.logging.Level;
import java.util.logging.LogManager;
import java.util.logging.Logger;

/**
 * TUI principal (Lanterna): fundo com o feed, janelas DOS por visão
 * (comentários, presentes, fixados, mensagens) e linha de comando ':'.
 * O mouse abre, move e fecha as janelas; os comandos fazem o mesmo.
 */
public final class TerminalUi implements EventSink, AutoCloseable {

    /** Tamanho do histórico buscado em {@code :pinned list} e no backfill. */
    private static final int PINNED_HISTORY = 20;

    /** Presentes mais recentes carregados por {@code :gift all}. */
    private static final int GIFT_HISTORY = 200;

    private final Screen screen;
    private final ExecutorService executor;
    private final String dbLabel;
    private final Instant startedAt = Instant.now();

    private final FeedBuffer feed = new FeedBuffer();
    private final WindowDesk desk = new WindowDesk();
    private final MenuBar menu = new MenuBar();
    private final Presenter presenter = new Presenter();
    private final Map<Category, Long> counters = new EnumMap<>(Category.class);
    private final Queue<Runnable> pending = new ConcurrentLinkedQueue<>();

    private ConnectLiveUseCase connectUseCase;
    private DisconnectLiveUseCase disconnectUseCase;
    private MonitorHub hub;

    private volatile LiveMonitorState state = LiveMonitorState.offline();
    private volatile boolean closed;

    private volatile List<String> giftCatalog = List.of();

    private final StringBuilder input = new StringBuilder();
    private final Object screenLock = new Object();
    private boolean screenStopped;
    private PrintStream savedOut;
    private PrintStream savedErr;
    private String lastFrameFailure = "";

    public TerminalUi(Path dbFile, ExecutorService executor) throws IOException {
        this.dbLabel = dbFile == null ? "data/tlm-local.db" : dbFile.toString();
        this.executor = executor;
        DefaultTerminalFactory factory = new DefaultTerminalFactory();
        factory.setMouseCaptureMode(MouseCaptureMode.CLICK_RELEASE_DRAG);
        Screen created = factory.createScreen();
        created.startScreen();
        this.screen = created;
    }

    /** Liga os casos de uso e o hub de dados aos comandos da linha ':' da TUI. */
    public void bind(ConnectLiveUseCase connectUseCase, DisconnectLiveUseCase disconnectUseCase,
                     MonitorHub hub) {
        this.connectUseCase = connectUseCase;
        this.disconnectUseCase = disconnectUseCase;
        this.hub = hub;
    }

    // ---------------------------------------------------------------- EventSink

    @Override
    public void onDomainEvent(DomainEvent event) {
        post(() -> {
            FeedLine line = presenter.line(event);
            feed.add(line);
            desk.publish(line);
            counters.merge(categoryOf(event), 1L, Long::sum);
        });
    }

    @Override
    public void onStateChanged(LiveMonitorState newState) {
        post(() -> this.state = newState);
    }

    @Override
    public void onNotice(String message) {
        post(() -> feed.add(new FeedLine(message, Category.INFO)));
    }

    @Override
    public void onAlert(String message) {
        post(() -> feed.add(new FeedLine(message, Category.SYSTEM)));
    }

    // ---------------------------------------------------------------- loop principal

    /** Loop da UI: roda na thread chamadora até {@code :quit}/Ctrl+C/EOF. */
    public void run() {
        silenceConsole();
        while (!closed) {
            try {
                drainPending();
                KeyStroke key = screen.pollInput();
                if (key != null) {
                    handleKey(key);
                }
                screen.doResizeIfNecessary();
                draw();
                // DELTA manda só as células que mudaram. COMPLETE a cada frame
                // apaga e reescreve o terminal inteiro e a tela fica piscando.
                screen.setCursorPosition(null);
                screen.refresh(Screen.RefreshType.DELTA);
                Thread.sleep(30);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            } catch (IOException e) {
                break;
            } catch (RuntimeException error) {
                noteFrameFailure(error);
                try {
                    Thread.sleep(30);
                } catch (InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    break;
                }
            }
        }
        stopScreenOnce();
    }

    public void requestQuit() {
        closed = true;
        drainPending();
    }

    @Override
    public void close() {
        closed = true;
        // Garante a restauração do terminal mesmo que o encerramento venha do
        // shutdown hook (SIGTERM/SIGINT externo), quando run() pode não
        // alcançar a saída normal.
        stopScreenOnce();
    }

    private void stopScreenOnce() {
        synchronized (screenLock) {
            if (screenStopped) {
                return;
            }
            screenStopped = true;
        }
        try {
            screen.stopScreen();
        } catch (IOException | IllegalStateException e) {
            // terminal já fechado ou nunca inicializado
        }
        restoreConsole();
    }

    /**
     * A biblioteca e o JUL escrevem no stderr real. No modo privado isso
     * rabisca a tela; o refresh em DELTA não corrige células que não mudaram.
     */
    private void silenceConsole() {
        LogManager.getLogManager().reset();
        Logger.getLogger("").setLevel(Level.OFF);
        savedOut = System.out;
        savedErr = System.err;
        PrintStream sink = new PrintStream(OutputStream.nullOutputStream());
        System.setOut(sink);
        System.setErr(sink);
    }

    private void restoreConsole() {
        if (savedOut != null) {
            System.setOut(savedOut);
            savedOut = null;
        }
        if (savedErr != null) {
            System.setErr(savedErr);
            savedErr = null;
        }
    }

    private void noteFrameFailure(RuntimeException error) {
        String message = error.getMessage();
        if (message == null || message.isBlank()) {
            message = error.getClass().getSimpleName();
        }
        if (message.equals(lastFrameFailure)) {
            return;
        }
        lastFrameFailure = message;
        feed.add(new FeedLine("quadro da tela: " + message, Category.SYSTEM));
    }

    private void drainPending() {
        Runnable task;
        while ((task = pending.poll()) != null) {
            task.run();
        }
    }

    private void post(Runnable task) {
        pending.offer(task);
    }

    // ---------------------------------------------------------------- teclado

    private void handleKey(KeyStroke key) {
        if (key.getKeyType() == KeyType.MouseEvent && key instanceof MouseAction mouse) {
            handleMouse(mouse);
            return;
        }
        if (key.isCtrlDown() && key.getCharacter() != null && key.getCharacter() == 'c') {
            requestQuit();
            return;
        }
        if (key.isCtrlDown() && key.getCharacter() != null && key.getCharacter() == 'w') {
            desk.closeFocused();
            return;
        }
        if (key.getKeyType() == KeyType.Tab) {
            desk.cycleFocus();
            return;
        }
        switch (key.getKeyType()) {
            case Enter -> submitInput();
            case Escape -> {
                if (input.isEmpty()) {
                    desk.closeFocused();
                } else {
                    input.setLength(0);
                }
            }
            case Backspace -> {
                if (input.length() > 0) {
                    input.deleteCharAt(input.length() - 1);
                }
            }
            case PageUp -> scrollPage(-1);
            case PageDown -> scrollPage(1);
            case ArrowUp -> scrollLines(-1);
            case ArrowDown -> scrollLines(1);
            case Character -> {
                Character character = key.getCharacter();
                if (character != null && character.charValue() >= 32) {
                    input.append(character.charValue());
                }
            }
            default -> {
            }
        }
    }

    private int feedRows() {
        TerminalSize size = screen.getTerminalSize();
        return Math.max(1, size.getRows() - 4);
    }

    private void scrollLines(int delta) {
        if (desk.focused().isPresent()) {
            desk.focused().get().scrollLines(delta);
            return;
        }
        if (delta < 0) {
            feed.pageUp(-delta);
        } else {
            feed.pageDown(delta);
        }
    }

    private void scrollPage(int pages) {
        TerminalSize size = screen.getTerminalSize();
        if (desk.focused().isPresent()) {
            desk.focused().get().scrollPage(pages, size.getColumns(), size.getRows());
            return;
        }
        if (pages < 0) {
            feed.pageUp(feedRows());
        } else {
            feed.pageDown(feedRows());
        }
    }

    private void handleMouse(MouseAction mouse) {
        TerminalSize size = screen.getTerminalSize();
        int cols = size.getColumns();
        int rows = size.getRows();
        int col = mouse.getPosition().getColumn();
        int row = mouse.getPosition().getRow();
        switch (mouse.getActionType()) {
            case CLICK_DOWN -> menu.hit(col, row, cols).ifPresentOrElse(
                    this::openFromMenu,
                    () -> desk.onClick(col, row, cols, rows));
            case DRAG -> desk.onDrag(col, row, cols, rows);
            case SCROLL_UP -> {
                if (!desk.onScroll(col, row, -1, cols, rows)) {
                    feed.pageUp(1);
                }
            }
            case SCROLL_DOWN -> {
                if (!desk.onScroll(col, row, 1, cols, rows)) {
                    feed.pageDown(1);
                }
            }
            default -> {
            }
        }
    }

    private void openFromMenu(WindowKind kind) {
        switch (kind) {
            case GIFTS -> openGifts(List.of(), false);
            case PINNED -> handlePinned("");
            case HISTORY -> openGiftHistory();
            default -> openLive(kind, "");
        }
    }

    private void submitInput() {
        String typed = input.toString();
        input.setLength(0);
        CommandParser.parse(typed).ifPresent(this::dispatch);
    }

    private void dispatch(CommandParser.Command command) {
        switch (command) {
            case CommandParser.Connect connect -> executor.submit(() ->
                    runSafely(() -> connectUseCase.execute(connect.username())));
            case CommandParser.Disconnect disconnect -> executor.submit(() ->
                    runSafely(() -> disconnectUseCase.execute(disconnect.username())));
            case CommandParser.Gift gift -> handleGift(gift.argument());
            case CommandParser.Pinned pinned -> handlePinned(pinned.argument());
            case CommandParser.Filter filter -> handleFilter(filter.category());
            case CommandParser.Open open -> openLive(open.kind(), open.argument());
            case CommandParser.Close ignored -> closeFocused();
            case CommandParser.UserFilter user -> applyUserFilter(user.user());
            case CommandParser.Quantity quantity -> applyQuantity(quantity.minimum());
            case CommandParser.Clear ignored -> feed.clear();
            case CommandParser.Help ignored -> showHelp();
            case CommandParser.Quit ignored -> requestQuit();
            case CommandParser.Unknown unknown -> feed.add(new FeedLine(unknown.message(), Category.SYSTEM));
        }
    }

    private void handleGift(String argument) {
        String arg = argument == null ? "" : argument.trim();
        if (arg.isEmpty() || arg.equalsIgnoreCase("lista") || arg.equalsIgnoreCase("list")) {
            openGifts(List.of(), false);
            fetchGiftCatalog();
            return;
        }
        if (arg.equalsIgnoreCase("all")) {
            openGiftHistory();
            return;
        }
        if (arg.equalsIgnoreCase("todos") || arg.equals("*")) {
            DosWindow window = desk.open(WindowKind.GIFTS);
            desk.setGifts(window, List.of());
            desk.setMinCount(window, 0);
            note("janela de presentes sem filtro de nome nem quantidade");
            return;
        }
        GiftSelection.Result result = GiftSelection.resolve(arg, giftCatalog);
        if (!result.ok()) {
            feed.add(new FeedLine(result.error(), Category.SYSTEM));
            return;
        }
        openGifts(result.names(), true);
        note("presentes: " + String.join(", ", result.names()) + " — janela filtrada");
    }

    private void openGifts(List<String> names, boolean replaceFilter) {
        boolean created = desk.find(WindowKind.GIFTS).isEmpty();
        DosWindow window = desk.open(WindowKind.GIFTS);
        if (replaceFilter) {
            desk.setGifts(window, names);
            return;
        }
        if (created) {
            note("janela aberta: presentes");
        }
    }

    private void openLive(WindowKind kind, String user) {
        boolean created = desk.find(kind).isEmpty();
        DosWindow window = desk.open(kind);
        if (user != null && !user.isBlank()) {
            desk.setUser(window, user);
            note(label(kind) + " — usuário @" + window.user());
            return;
        }
        if (created) {
            note("janela aberta: " + label(kind));
        }
    }

    private void closeFocused() {
        if (desk.focused().isEmpty()) {
            note("nenhuma janela em foco — clique numa ou use o menu");
            return;
        }
        desk.closeFocused();
    }

    private void applyUserFilter(String user) {
        Optional<DosWindow> focused = desk.focused();
        if (focused.isEmpty()) {
            note("foque uma janela (clique ou Tab) antes de :user");
            return;
        }
        desk.setUser(focused.get(), user);
        note(user == null || user.isBlank()
                ? "filtro de usuário removido"
                : "usuário: @" + focused.get().user());
    }

    private void applyQuantity(int minimum) {
        DosWindow window = desk.open(WindowKind.GIFTS);
        desk.setMinCount(window, minimum);
        note(minimum == 0 ? "quantidade: qualquer" : "quantidade mínima: " + minimum);
    }

    private static String label(WindowKind kind) {
        return kind.title().trim().toLowerCase();
    }

    private void note(String text) {
        feed.add(new FeedLine(text, Category.INFO));
    }

    private void fetchGiftCatalog() {
        executor.submit(() -> runSafely(() -> {
            List<String> catalog = hub.availableGifts();
            giftCatalog = catalog;
            post(() -> {
                if (catalog.isEmpty()) {
                    feed.add(new FeedLine(
                            "catálogo indisponível — conecte a uma live e tente de novo em alguns segundos",
                            Category.INFO));
                    return;
                }
                for (int i = 0; i < catalog.size(); i++) {
                    feed.add(new FeedLine((i + 1) + ". " + catalog.get(i), Category.INFO));
                }
                feed.add(new FeedLine(catalog.size()
                        + " presentes; escolha com :gift <números ou nomes> (ex.: :gift 1,4)", Category.INFO));
            });
        }));
    }

    /**
     * Abre a janela com os presentes já gravados da live conectada.
     * O filtro do feed de fundo permanece como estava.
     */
    private void openGiftHistory() {
        int generation = desk.beginHistory();
        String live = state.username() == null ? "" : state.username().trim();
        if (live.isEmpty()) {
            desk.showHistory(generation, " PRESENTES ", " [X] fecha ",
                    List.of(new FeedLine("conecte a uma live para ver os presentes", Category.INFO)));
            return;
        }
        if (hub == null) {
            desk.showHistory(generation, " PRESENTES @" + live + " ", " [X] fecha ",
                    List.of(new FeedLine("banco local indisponível", Category.INFO)));
            return;
        }
        String title = " PRESENTES @" + live + " ";
        desk.showHistory(generation, title, " [X] fecha ",
                List.of(new FeedLine("carregando...", Category.INFO)));
        executor.submit(() -> {
            try {
                List<GiftEntry> entries = hub.recentGifts(live, GIFT_HISTORY);
                post(() -> {
                    if (entries.isEmpty()) {
                        desk.showHistory(generation, title, " [X] fecha   0 envios ",
                                List.of(new FeedLine("nenhum presente nesta live", Category.INFO)));
                        return;
                    }
                    String footer = " setas rolam   [X] fecha   " + entries.size() + " envios ";
                    desk.showHistory(generation, title, footer, rowsOf(entries));
                });
            } catch (MonitorException error) {
                post(() -> desk.showHistory(generation, title, " [X] fecha ",
                        List.of(new FeedLine(error.getMessage(), Category.SYSTEM))));
            } catch (RuntimeException runtime) {
                post(() -> desk.showHistory(generation, title, " [X] fecha ",
                        List.of(new FeedLine("Erro inesperado: " + runtime.getMessage(), Category.SYSTEM))));
            }
        });
    }

    private static List<FeedLine> rowsOf(List<GiftEntry> newestFirst) {
        List<GiftEntry> chronological = new ArrayList<>(newestFirst);
        Collections.reverse(chronological);
        List<FeedLine> rows = new ArrayList<>();
        for (GiftEntry entry : chronological) {
            String who = entry.nickname() == null || entry.nickname().isBlank() ? "?" : entry.nickname();
            String stamped = pinnedStamp(entry.timestamp()).trim();
            String when = stamped.isEmpty() ? "--:--" : stamped;
            String name = entry.giftName() == null || entry.giftName().isBlank() ? "um presente" : entry.giftName();
            String gift = entry.count() > 1 ? name + " x" + entry.count() : name;
            rows.add(new FeedLine(when + "  @" + who + "  " + gift, Category.GIFT,
                    entry.giftName(), who, entry.count()));
        }
        return rows;
    }

    private void handlePinned(String argument) {
        String arg = argument == null ? "" : argument.trim().toLowerCase();
        DosWindow window = desk.open(WindowKind.PINNED);
        boolean refresh = arg.equals("list");
        if (refresh || !window.hasCategory(Category.PINNED)) {
            fetchPinnedInto(refresh);
        }
    }

    private void handleFilter(Category category) {
        if (category == Category.PINNED) {
            applyPinnedFilter();
            return;
        }
        applyFilter(category);
    }

    /**
     * Filtro de fixados com backfill: sem fixados ao vivo no feed, busca os
     * últimos do banco local (marcados como histórico) e então aplica o filtro.
     */
    private void applyPinnedFilter() {
        if (feed.hasCategory(Category.PINNED)) {
            applyFilter(Category.PINNED);
            return;
        }
        executor.submit(() -> runSafely(() -> {
            String live = state.username();
            List<PinnedCommentEntry> entries = hub.pinnedComments(live, PINNED_HISTORY);
            boolean allLives = live.isBlank();
            post(() -> {
                if (entries.isEmpty()) {
                    feed.add(new FeedLine("nenhum comentário fixado no histórico local", Category.INFO));
                } else {
                    feed.add(new FeedLine(entries.size() + " fixados do histórico:", Category.INFO));
                    for (PinnedCommentEntry entry : entries) {
                        feed.add(new FeedLine(describePinned(entry, allLives) + " (histórico)", Category.PINNED));
                    }
                }
                applyFilter(Category.PINNED);
            });
        }));
    }

    private void fetchPinnedInto(boolean replace) {
        executor.submit(() -> runSafely(() -> {
            String live = state.username();
            List<PinnedCommentEntry> entries = hub.pinnedComments(live, PINNED_HISTORY);
            boolean allLives = live == null || live.isBlank();
            post(() -> desk.find(WindowKind.PINNED).ifPresent(window -> {
                if (entries.isEmpty()) {
                    note("nenhum comentário fixado no histórico local");
                    return;
                }
                List<FeedLine> lines = new ArrayList<>();
                for (PinnedCommentEntry entry : entries) {
                    String who = entry.nickname() == null || entry.nickname().isBlank()
                            ? (entry.uniqueId() == null ? "" : entry.uniqueId())
                            : entry.nickname();
                    lines.add(new FeedLine(describePinned(entry, allLives), Category.PINNED, "", who, 0));
                }
                if (replace) {
                    desk.replaceInjected(window, lines);
                } else {
                    desk.inject(window, lines);
                }
                note(lines.size() + " fixados na janela");
            }));
        }));
    }

    private static String describePinned(PinnedCommentEntry entry, boolean allLives) {
        String nick = entry.nickname();
        String who = nick == null || nick.isBlank()
                ? (entry.uniqueId() == null ? "" : entry.uniqueId())
                : nick;
        String liveName = entry.liveName() == null ? "" : entry.liveName();
        String liveAt = allLives && !liveName.isBlank() ? "@" + liveName + " " : "";
        String when = pinnedStamp(entry.timestamp());
        return "[fixado" + when + "] " + liveAt + "@" + who + ": " + entry.comment();
    }

    /** Melhor esforço de exibição " HH:mm" do timestamp ISO; inválido → "". */
    private static String pinnedStamp(String timestamp) {
        if (timestamp == null || timestamp.isBlank()) {
            return "";
        }
        try {
            Instant at = OffsetDateTime.parse(timestamp).toInstant();
            return " " + DateTimeFormatter.ofPattern("HH:mm").withZone(ZoneId.systemDefault()).format(at);
        } catch (RuntimeException e) {
            return "";
        }
    }

    private void applyFilter(Category category) {
        if (category == null) {
            feed.setFilter(line -> true);
            feed.add(new FeedLine("filtro: all", Category.INFO));
        } else {
            feed.setFilter(line -> line.category() == category);
            feed.add(new FeedLine("filtro: " + category.name().toLowerCase(), Category.INFO));
        }
    }

    private void showHelp() {
        for (String line : CommandParser.helpText().split("\\n")) {
            feed.add(new FeedLine(line, Category.INFO));
        }
    }

    private void runSafely(LocalCallable callable) {
        try {
            callable.run();
        } catch (MonitorException error) {
            post(() -> feed.add(new FeedLine(error.getMessage(), Category.SYSTEM)));
        } catch (RuntimeException runtime) {
            post(() -> feed.add(new FeedLine("Erro inesperado: " + runtime.getMessage(), Category.SYSTEM)));
        }
    }

    @FunctionalInterface
    private interface LocalCallable {
        void run() throws MonitorException;
    }

    // ---------------------------------------------------------------- desenho

    private void draw() {
        TerminalSize size = screen.getTerminalSize();
        TextGraphics g = screen.newTextGraphics();
        g.setBackgroundColor(TextColor.ANSI.DEFAULT);
        g.fillRectangle(new TerminalPosition(0, 0), size, ' ');

        int rows = size.getRows();
        int cols = size.getColumns();
        if (rows < 4 || cols < 20) {
            g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
            g.putString(0, Math.max(0, rows / 2), "terminal pequeno demais");
            return;
        }

        drawHeader(g, rows, cols);
        drawMenu(g, cols);
        drawFeed(g, rows, cols);
        drawStats(g, rows, cols);
        for (DosWindow window : desk.stack()) {
            window.draw(g, cols, rows);
        }
        drawInput(g, rows, cols);
    }

    private void drawHeader(TextGraphics g, int rows, int cols) {
        LiveMonitorState st = state;
        String status;
        TextColor statusColor;
        if (st.connected()) {
            status = "Monitorando " + st.displayName();
            statusColor = TextColor.ANSI.GREEN_BRIGHT;
        } else {
            status = "Desconectado";
            statusColor = TextColor.ANSI.RED_BRIGHT;
        }
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.putString(0, 0, "TikTok Live Monitor (local)");
        g.setForegroundColor(statusColor);
        g.putString(28, 0, TerminalText.fit(status, cols - 29));
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        String uptime = "no ar " + formatUptime(Duration.between(startedAt, Instant.now()));
        g.putString(Math.max(0, cols - uptime.length()), 0, uptime);

    }

    private void drawMenu(TextGraphics g, int cols) {
        g.setForegroundColor(TextColor.ANSI.BLACK);
        g.setBackgroundColor(TextColor.ANSI.WHITE);
        g.putString(0, 1, " ".repeat(Math.max(0, cols)));
        for (MenuBar.Item item : menu.layout(cols)) {
            g.putString(item.x(), 1, item.label());
        }
        g.setBackgroundColor(TextColor.ANSI.DEFAULT);
    }

    private void drawFeed(TextGraphics g, int rows, int cols) {
        int available = Math.max(0, rows - 4);
        List<FeedLine> window = feed.window(available);
        int y = 2 + Math.max(0, available - window.size());
        for (FeedLine line : window) {
            g.setForegroundColor(colorOf(line.category()));
            g.putString(0, y, TerminalText.fit(line.text(), cols));
            y++;
        }
        if (!feed.atBottom()) {
            g.setForegroundColor(TextColor.ANSI.CYAN_BRIGHT);
            g.putString(cols - 2, 2, "▲");
        }
    }

    private void drawStats(TextGraphics g, int rows, int cols) {
        long comments = counters.getOrDefault(Category.CHAT, 0L);
        long gifts = counters.getOrDefault(Category.GIFT, 0L);
        long likes = counters.getOrDefault(Category.LIKE, 0L);
        long followers = counters.getOrDefault(Category.FOLLOW, 0L);
        long pinned = counters.getOrDefault(Category.PINNED, 0L);
        String focus = desk.focused().map(window -> "   foco " + window.title().trim()).orElse("");
        String stats = String.format(
                "comentários %d   presentes %d   curtidas %d   seguidores %d   fixados %d%s   %s",
                comments, gifts, likes, followers, pinned, focus, dbLabel);
        g.setForegroundColor(TextColor.ANSI.BLACK_BRIGHT);
        g.putString(0, rows - 2, TerminalText.fit(stats, cols));
    }

    private void drawInput(TextGraphics g, int rows, int cols) {
        String line = ": " + input;
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.putString(0, rows - 1, TerminalText.fit(line, Math.max(0, cols - 2)));
        g.setBackgroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.putString(Math.min(cols - 2, line.length()), rows - 1, " ");
        g.setBackgroundColor(TextColor.ANSI.DEFAULT);
    }

    private TextColor colorOf(Category category) {
        return switch (category) {
            case CHAT -> TextColor.ANSI.CYAN_BRIGHT;
            case GIFT -> TextColor.ANSI.YELLOW_BRIGHT;
            case LIKE -> TextColor.ANSI.MAGENTA_BRIGHT;
            case FOLLOW -> TextColor.ANSI.GREEN_BRIGHT;
            case PINNED -> TextColor.ANSI.BLUE_BRIGHT;
            case SYSTEM -> TextColor.ANSI.RED_BRIGHT;
            case INFO -> TextColor.ANSI.WHITE_BRIGHT;
        };
    }

    private Category categoryOf(DomainEvent event) {
        return switch (event) {
            case ChatMessage m -> Category.CHAT;
            case GiftEvent g -> Category.GIFT;
            case LikeEvent l -> Category.LIKE;
            case FollowEvent f -> Category.FOLLOW;
            case PinnedCommentEvent p -> Category.PINNED;
            case SystemNotice n -> Category.SYSTEM;
        };
    }

    private static String formatUptime(Duration duration) {
        long total = Math.max(0, duration.getSeconds());
        long h = total / 3600;
        long m = (total % 3600) / 60;
        long s = total % 60;
        return String.format("%02d:%02d:%02d", h, m, s);
    }
}
