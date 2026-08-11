package moderation

import (
	"context"
	"fmt"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// baseSystemPrompt is the core moderation system prompt for the LLM.
const baseSystemPrompt = `Você é moderador de chat de live em português do Brasil. Avalie o comentário isolado e o contexto recente.

ATENÇÃO: É comum usuários responderem a outros citando o nome no início (ex: "@JesusTeAma qual sua religião?"). IGNORE nomes de usuário ou arrobas (@) na avaliação. Foque apenas na intenção da mensagem real.

DIRETRIZES DE CLASSIFICAÇÃO:
- NAO: Mensagem comum, saudação, elogio neutro ou comentário irrelevante.
- SIM_PERGUNTA: Qualquer pergunta direta ou dúvida enviada ao streamer ou sobre o tema da live.
- PRIORIDADE: Se a mensagem for uma pergunta e NÃO tiver xingamento/ofensa explícita, classifique como SIM_PERGUNTA (nunca SIM_ODIO).
- SIM_PROSELITISMO: Pregação, tentativa de conversão, "Jesus te ama", "Aceite a Cristo", condenação religiosa ou imposição de dogmas.
- SIM_ODIO: Ofensa, ataque pessoal, xingamento, humilhação, racismo ou preconceito.

Responda com EXATAMENTE UMA destas palavras-chave (maiúsculas):
- NAO
- SIM_PERGUNTA
- SIM_PROSELITISMO
- SIM_ODIO
- SIM_SPAM
- SIM_GOLPE
- SIM_OUTRO

Regra de saída: uma única token, sem explicações.`

// buildPromptContext builds the system prompt enriched with user feedback examples.
func buildPromptContext(ctx context.Context, repo model.Repository, limit int) (string, int, error) {
	if repo == nil {
		return baseSystemPrompt, 0, nil
	}

	feedbacks, err := repo.GetRecentFeedbacks(limit)
	if err != nil {
		return baseSystemPrompt, 0, fmt.Errorf("get feedbacks: %w", err)
	}

	if len(feedbacks) == 0 {
		return baseSystemPrompt, 0, nil
	}

	var sb strings.Builder
	sb.WriteString(baseSystemPrompt)
	sb.WriteString("\n\nO usuário humano revisou as seguintes mensagens. Siga estas classificações como exemplos (Few-Shot):\n")
	for _, f := range feedbacks {
		sb.WriteString(fmt.Sprintf("- Texto: %q -> Classificar como: %s\n", f.Comment, f.Expected))
	}

	return sb.String(), len(feedbacks), nil
}
