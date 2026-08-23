"""Montagem do texto de contexto da live a partir do buffer (SRP).

A apresentação (texto para o prompt) fica separada do armazenamento
e da detecção de repetição que vivem em ``buffer.MessageBuffer``.
"""

from .buffer import LiveBuffer


class ContextBuilder:
    """Constrói o contexto textual da live para alimentar o LLM."""

    def __init__(self, buffer: LiveBuffer):
        self.buffer = buffer

    def build(self, message_limit: int = 50) -> str:
        lines = []
        participants = self.buffer.active_participants()
        lines.append(f"Total de mensagens registradas: {self.buffer.total_messages()}")
        lines.append(f"Participantes ativos: {len(participants)}")

        lines.append("\nMensagens recentes:")
        recent = self.buffer.recent_messages(message_limit)
        if recent:
            for m in recent:
                name = m["nickname"] or m["uniqueId"] or "?"
                lines.append(f"- {name}: {m['comment']}")
        else:
            lines.append("- (nenhuma mensagem)")

        lines.append("\nRepetições de mensagens:")
        reps = self.buffer.top_repetitions(10)
        if reps:
            for r in reps:
                name = r["nickname"] or r["uniqueId"] or "?"
                lines.append(f"- ({r['count']}x) {name}: {r['comment']}")
        else:
            lines.append("- (nenhuma repetição detectada)")

        lines.append("\nPresentes enviados:")
        gifts = self.buffer.top_gifts(10)
        if gifts:
            for g in gifts:
                lines.append(f"- {g['nickname']}: {g['giftName']} x{g['count']}")
        else:
            lines.append("- (nenhum presente registrado)")

        lines.append("\nModeração (eventos flagados):")
        flagged = self.buffer.recent_flagged(20)
        if flagged:
            for f in flagged:
                name = f["nickname"] or f["uniqueId"] or "?"
                lines.append(f"- [{f['category']}] {name}: {f['comment']}")
        else:
            lines.append("- (nenhum conteúdo flagado)")

        return "\n".join(lines)
