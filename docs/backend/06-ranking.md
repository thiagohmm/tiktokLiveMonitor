# Pacote `ranking` — Cálculo de rankings de participação

> Diretório: `backend/internal/ranking/`

Pacote puro (sem I/O): recebe os agregados por usuário (`[]model.LiveStat`) e
produz o ranking ordenado (`[]ScoreResult` / `model.LiveRanking`). Há **dois
modos**:

1. **`engagement` (padrão)** — pontuação ponderada: presentes > shares >
   likes > perguntas, com penalidade para anomalias (spam/repetição).
2. **`tiktok`** — reproduz o ranking **da sala do TikTok**: os usuários são
   ordenados apenas pelo valor (💎) dos presentes enviados; os 3 primeiros
   recebem tiers visuais (coroa/faixa/medalha).

Arquivo: `backend/internal/ranking/ranking.go`

---

## Pesos (`Weights`) e default

| Campo | Padrão | Papel |
|---|---|---|
| `GiftPointPerGift` | 20 | Pontos por **presente** recebido. |
| `GiftPointPerUnit` | 1 | Pontos por **moeda (💎)** de valor total de presente. |
| `MessagePointPerMessage` | 0 | Pontos por mensagem — **0** porque mensagens não são critério de ranking. |
| `QuestionPointPerQuestion` | 2 | Pontos extras por pergunta detectada. |
| `SharePointPerShare` | 10 | Pontos por compartilhamento. |
| `LikePointPerLike` | 3 | Pontos por curtida enviada. |
| `AnomalyPenaltyPerEvent` | 3 | **Subtrai** pontos por evento de anomalia (mensagem repetida/spam). |

`DefaultWeights` = valores acima. Prioridade: **presentes > shares > likes >
perguntas**. Mensagens chat não decidem o topo do ranking.

---

## Tipos

| Tipo | Descrição |
|---|---|
| `ScoreResult` | Resultado por participante: `UserRank`, `GiftScore`, `Score`, `RiskLevel`. |
| `Ranker` | Guarda os `Weights`. |

### Erro
```go
ErrUnparseableTimestamp // "ranking: unparseable timestamp"
```

---

## Funções

| Função | Descrição |
|---|---|
| `New(weights Weights) *Ranker` | Cria um `Ranker`; se os pesos estiverem zerados (todos os principais), usa `DefaultWeights`. |
| `Compute(stats []LiveStat, anomaliesByUser map[string]int) []ScoreResult` | **Modo engagement.** Para cada usuário: `giftScore = 20×GiftCount + 1×GiftValue`; `score = giftScore + messageScore + questionScore + shareScore + likeScore − anomalias×3` (mínimo 0). Preenche `UserRank` (incluindo `Diamonds = GiftValue`, `AnomalyCount`, `RiskLevel`) e ordena por score desc (desempate por nickname). |
| `BuildLiveRanking(liveName, stats, anomaliesByUser) LiveRanking` | Monta o `LiveRanking` completo em modo `engagement`, com `TotalGiftValue` (soma da sala) e `UpdatedAt` UTC. |
| `ComputeTikTok(stats []LiveStat) []ScoreResult` | **Modo TikTok.** Score = `Diamonds` (valor em 💎). Ordena por diamantes (desempate: nº de presentes, depois nickname). Atribui tiers ao top 3: `crown` (1º), `headband` (2º), `medal` (3º). |
| `BuildTikTokRanking(liveName, stats) LiveRanking` | Monta o `LiveRanking` em modo `tiktok` com `TotalGiftValue`. |
| `riskLevelFor(anomalyCount, score)` | Risco: ≥4 anomalias → critical; ≥2 → high; ==1 → medium; score > 0 → low; senão none. |
| `formatTS(raw)` / `trimRawTS(raw)` / `parseTS(raw)` | Normalizam timestamps armazenados para RFC3339 UTC. |

---

## Fórmulas (resumo)

**Engajamento**
```
giftScore   = 20 × giftCount + 1 × giftValue💎
score       = giftScore + 2×questions + 10×shares + 3×likes − 3×anomalias
score       = max(0, score)
ordem       = score desc, nickname asc
```

**TikTok**
```
score = giftValue💎 (= diamantes)
ordem = diamantes desc, giftCount desc, nickname asc
top3  = crown / headband / medal
```

> Onde `giftValue` vem de `model.GiftValue(gift_name) × repeat_count`,
> somado por usuário na query `LiveStatsByUser` (ver `02-database.md`).

---

## Diagramas relacionados
- `diagrams/00-arquitetura.puml` — quem chama o ranker.
- `diagrams/02-banco-er.puml` — tabelas que alimentam `LiveStatsByUser`.
