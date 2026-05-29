const { analyzeMessage, clearModerationCache } = require('./moderation');
const { completeModeration } = require('./ai');

// Mock do módulo ai
jest.mock('./ai', () => ({
  completeModeration: jest.fn()
}));

describe('Moderation Module', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    clearModerationCache();
  });

  describe('Filtros Regex - Ataque a religiões afro', () => {
    it('deve detectar ataque óbvio a religiões afro-brasileiras', async () => {
      const result = await analyzeMessage('candomblé é coisa do diabo', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('RELIGIAO');
    });

    it('deve detectar macumba associada a demônio', async () => {
      const result = await analyzeMessage('macumba é demoníaco', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('RELIGIAO');
    });

    it('deve detectar umbanda associada a satanás', async () => {
      const result = await analyzeMessage('umbanda é satanismo puro', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('RELIGIAO');
    });

    it('não deve flagrar menção neutra a religiões afro', async () => {
      completeModeration.mockResolvedValue('NAO');
      const result = await analyzeMessage('eu respeito o candomblé', 'user1', 'User1', []);
      expect(result.flagged).toBe(false);
    });
  });

  describe('Filtros Regex - Proselitismo cristão', () => {
    it('deve detectar "Jesus te ama"', async () => {
      const result = await analyzeMessage('Jesus te ama', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "Deus te ama"', async () => {
      const result = await analyzeMessage('Deus te ama', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "em nome de Jesus"', async () => {
      const result = await analyzeMessage('em nome de Jesus, pare com isso', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "glória a Deus"', async () => {
      const result = await analyzeMessage('glória a Deus', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "louvado seja"', async () => {
      const result = await analyzeMessage('louvado seja o Senhor', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "aleluia"', async () => {
      const result = await analyzeMessage('aleluia irmãos', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "amém"', async () => {
      const result = await analyzeMessage('amém', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "paz do senhor"', async () => {
      const result = await analyzeMessage('paz do senhor', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "aceita Jesus"', async () => {
      const result = await analyzeMessage('aceita Jesus na sua vida', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "Jesus salva"', async () => {
      const result = await analyzeMessage('Jesus salva', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('deve detectar "só Jesus salva"', async () => {
      const result = await analyzeMessage('só Jesus salva', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('PROSELITISMO');
    });

    it('não deve flagrar menção casual a Deus', async () => {
      completeModeration.mockResolvedValue('NAO');
      const result = await analyzeMessage('meu Deus, que incrível', 'user1', 'User1', []);
      expect(result.flagged).toBe(false);
    });
  });

  describe('Filtros Regex - Spam e Golpes', () => {
    it('deve detectar links externos (bit.ly)', async () => {
      completeModeration.mockResolvedValue('SIM_SPAM');
      const result = await analyzeMessage('clique em bit.ly/promocao', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('SPAM');
    });

    it('deve detectar links wa.me', async () => {
      completeModeration.mockResolvedValue('SIM_SPAM');
      const result = await analyzeMessage('me chama no wa.me/5511999999999', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('SPAM');
    });

    it('deve detectar "pix qrcode"', async () => {
      completeModeration.mockResolvedValue('SIM_GOLPE');
      const result = await analyzeMessage('pix qrcode para ganhar dinheiro', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
    });

    it('deve detectar "ganhe dinheiro fácil"', async () => {
      completeModeration.mockResolvedValue('SIM_GOLPE');
      const result = await analyzeMessage('ganhe dinheiro fácil em casa', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
    });

    it('não deve flagrar link do TikTok', async () => {
      completeModeration.mockResolvedValue('NAO');
      const result = await analyzeMessage('olha esse vídeo tiktok.com/@user/video', 'user1', 'User1', []);
      expect(result.flagged).toBe(false);
    });
  });

  describe('Filtros Regex - Ataques pessoais', () => {
    it('deve detectar insulto direto "você é idiota"', async () => {
      completeModeration.mockResolvedValue('SIM_ODIO');
      const result = await analyzeMessage('você é idiota', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('ODIO');
    });

    it('deve detectar "vai tomar no cu"', async () => {
      completeModeration.mockResolvedValue('SIM_ODIO');
      const result = await analyzeMessage('vai tomar no cu', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('ODIO');
    });

    it('deve detectar "cala a boca"', async () => {
      completeModeration.mockResolvedValue('SIM_ODIO');
      const result = await analyzeMessage('cala a boca', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('ODIO');
    });

    it('deve detectar "morre"', async () => {
      completeModeration.mockResolvedValue('SIM_ODIO');
      const result = await analyzeMessage('morre', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('ODIO');
    });

    it('deve detectar gíria regional ofensiva "testudo"', async () => {
      completeModeration.mockResolvedValue('SIM_ODIO');
      const result = await analyzeMessage('seu testudo', 'user1', 'User1', []);
      expect(result.flagged).toBe(true);
      expect(result.category).toBe('ODIO');
    });

    it('não deve flagrar uso neutro de "enganado"', async () => {
      completeModeration.mockResolvedValue('NAO');
      const result = await analyzeMessage('fui enganado na compra', 'user1', 'User1', []);
      expect(result.flagged).toBe(false);
    });
  });

  describe('Cache de moderação', () => {
    it('deve usar cache para mensagens idênticas', async () => {
      completeModeration.mockResolvedValue('SIM_SPAM');
      
      // Mensagem que passa pelo spam gate (tem link externo)
      const result1 = await analyzeMessage('clique em bit.ly/spam agora', 'user1', 'User1', []);
      const result2 = await analyzeMessage('clique em bit.ly/spam agora', 'user2', 'User2', []);
      
      expect(result1).toEqual(result2);
      expect(completeModeration).toHaveBeenCalledTimes(1);
    });

    it('deve limpar cache quando clearModerationCache é chamado', async () => {
      completeModeration.mockResolvedValue('SIM_SPAM');
      
      await analyzeMessage('clique em bit.ly/spam agora', 'user1', 'User1', []);
      clearModerationCache();
      await analyzeMessage('clique em bit.ly/spam agora', 'user2', 'User2', []);
      
      expect(completeModeration).toHaveBeenCalledTimes(2);
    });
  });

  describe('Cooldown de moderação', () => {
    it('deve entrar em cooldown após erro na IA', async () => {
      completeModeration.mockRejectedValue(new Error('Erro na IA'));
      
      // Mensagem que passa pelo gate (ataque pessoal)
      const result = await analyzeMessage('você é um idiota', 'user1', 'User1', []);
      expect(result.flagged).toBe(false);
      
      // Segunda chamada não deve chamar a IA (cooldown)
      const result2 = await analyzeMessage('você é um idiota também', 'user2', 'User2', []);
      expect(result2.flagged).toBe(false);
      expect(completeModeration).toHaveBeenCalledTimes(1);
    });
  });

  describe('Mensagens normais', () => {
    it('não deve flagrar "oi, tudo bem?"', async () => {
      const result = await analyzeMessage('oi, tudo bem?', 'user1', 'User1', []);
      expect(result.flagged).toBe(false);
    });

    it('não deve flagrar "boa live!"', async () => {
      const result = await analyzeMessage('boa live!', 'user1', 'User1', []);
      expect(result.flagged).toBe(false);
    });

    it('não deve flagrar "kkkkkk"', async () => {
      const result = await analyzeMessage('kkkkkk', 'user1', 'User1', []);
      expect(result.flagged).toBe(false);
    });

    it('não deve flagrar emoji', async () => {
      const result = await analyzeMessage('❤️🔥', 'user1', 'User1', []);
      expect(result.flagged).toBe(false);
    });
  });
});
