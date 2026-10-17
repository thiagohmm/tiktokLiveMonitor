package com.tlm.cli.application;

import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.LiveMonitorState;
import com.tlm.cli.domain.model.MonitorException;
import com.tlm.cli.domain.model.PinnedCommentEntry;
import com.tlm.cli.domain.port.EventSink;
import com.tlm.cli.domain.port.LocalStorePort;
import com.tlm.cli.domain.port.MonitorEnginePort;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

/**
 * Barramento de eventos da sessão local: a engine publica aqui (implementa
 * {@link EventSink}) e o hub reencaminha para os listeners (a UI). Também
 * expõe as consultas usadas pelos comandos (catálogo de presentes via engine,
 * histórico de fixados via store).
 */
public final class MonitorHub implements EventSink {

    private final CopyOnWriteArrayList<EventSink> listeners = new CopyOnWriteArrayList<>();

    private MonitorEnginePort engine;
    private LocalStorePort store;

    /** Injeta as portas depois de construído (a engine usa o hub como sink). */
    public void bind(MonitorEnginePort engine, LocalStorePort store) {
        this.engine = engine;
        this.store = store;
    }

    public void addListener(EventSink listener) {
        listeners.add(listener);
    }

    @Override
    public void onDomainEvent(DomainEvent event) {
        for (EventSink listener : listeners) {
            listener.onDomainEvent(event);
        }
    }

    @Override
    public void onStateChanged(LiveMonitorState state) {
        for (EventSink listener : listeners) {
            listener.onStateChanged(state);
        }
    }

    @Override
    public void onNotice(String message) {
        for (EventSink listener : listeners) {
            listener.onNotice(message);
        }
    }

    @Override
    public void onAlert(String message) {
        for (EventSink listener : listeners) {
            listener.onAlert(message);
        }
    }

    /** Atalhos usados pelos casos de uso. */
    public void notice(String message) {
        onNotice(message);
    }

    public void alert(String message) {
        onAlert(message);
    }

    /** Re-sincroniza o header a partir do estado real da engine. */
    public void refreshState() {
        MonitorEnginePort current = engine;
        if (current == null) {
            return;
        }
        String live = current.currentLive();
        onStateChanged(new LiveMonitorState(current.isConnected(), live, ""));
    }

    /** Catálogo de presentes da live atual (vazio enquanto não carregado). */
    public List<String> availableGifts() {
        MonitorEnginePort current = engine;
        return current == null ? List.of() : current.availableGifts();
    }

    /** Histórico local de fixados; {@code live} vazio traz todas as lives. */
    public List<PinnedCommentEntry> pinnedComments(String live, int limit) throws MonitorException {
        LocalStorePort current = store;
        if (current == null) {
            throw new MonitorException("banco local indisponível");
        }
        return current.recentPinned(live == null ? "" : live.trim(), limit);
    }
}
