# Pacote `report` — Relatório pós-live determinístico

> Diretório: `backend/internal/report/`

Gera o resumo **pós-live** (`model.LiveReport`) a partir dos dados persistidos.
É **determinístico** (sem IA): soma de eventos, principais apoiadores, perguntas
frequentes e resumo de moderação por categoria. A geração é *best-effort* — cada
fonte de dados que falha é registrada no log e o restante do relatório segue.

Arquivo: `backend/internal/report/report.go`

---

## Tipo principal

| Tipo | Descrição |
|---|---|
| `Generator` | Encapsula o repositório (`repo model.Repository`). |
| `New(repo model.Repository) *Generator` | Construtor. |

---

## Função principal

| Função | Descrição |
|---|---|
| `Generate(ctx context.Context, liveName string) (model.LiveReport, error)` | Monta o relatório: 1) `StartedAt` = `LiveFirstSeen`; `EndedAt` = agora; 2) estatísticas por usuário (`LiveStatsByUser`) → `ParticipantCount`, `GiftCount`, `MessageCount`, `TopSupporters`; 3) `GetRecentGifts(live, 1000)` → `GiftTotal` (unidades), `GiftValue` (💎), e sobrescreve `GiftCount` pelo número de eventos; 4) `GetAllUserMessages` → `FrequentQuestions`; 5) anomalias por live → `ModerationIssues`; 6) `DurationMinutes` entre início/fim. |

### Helpers privados

| Função | Descrição |
|---|---|
| `totalMessages(stats) int` | Soma `MessageCount` dos stats. |
| `topSupporters(stats) []UserRank` | Converte stats em `UserRank` (com `GiftScore`/`Diamonds` = giftValue), ordena por score desc e limita a **10**. |
| `giftTotal(gifts) int` | Soma `RepeatCount`. |
| `giftValueTotal(gifts) int` | Soma `RepeatCount × model.GiftValue(gift_name)` (valor em 💎). |
| `frequentQuestions(messages) []string` | Conta mensagens idênticas (≥3 chars) em todo o histórico; mantém as que aparecem **≥2 vezes**; ordena alfabeticamente desc; limita a 8. |
| `anomalySummary(logs) []AnomalySummary` | Agrupa logs com `IsAnomaly` por `Category` (contagem). |
| `durationMinutes(started, ended string) int` | Diferença em minutos (0 se inválido/negativo). |
| `sortUserRanksByScore(ranks)` | Selection sort por `GiftScore` desc. |
| `sortStringsDescByCount(questions)` | Selection sort alfabético desc. |

---

## Observações
- `ctx` hoje é aceito mas não usado internamente (interface pronta para
  cancelamento).
- `TopSupporters`, `FrequentQuestions` e `ModerationIssues` são heurísticas
  simples sobre os dados — não há chamada a LLM/IA neste pacote.

## Diagramas relacionados
- `diagrams/00-arquitetura.puml`.
