// Testes para funções puras do main.js
// Como main.js usa Electron, vamos testar apenas as funções que podem ser extraídas

describe('Main.js - Funções Puras', () => {
  describe('normalizeId', () => {
    // Função inline no main.js, vamos recriar para teste
    function normalizeId(value) {
      return String(value || '').toLowerCase();
    }

    it('deve normalizar para lowercase', () => {
      expect(normalizeId('UserName')).toBe('username');
    });

    it('deve lidar com null', () => {
      expect(normalizeId(null)).toBe('');
    });

    it('deve lidar com undefined', () => {
      expect(normalizeId(undefined)).toBe('');
    });

    it('deve lidar com string vazia', () => {
      expect(normalizeId('')).toBe('');
    });

    it('deve manter números', () => {
      expect(normalizeId('User123')).toBe('user123');
    });
  });

  describe('isTargetGift', () => {
    // Função inline no main.js, vamos recriar para teste
    function isTargetGift(giftName) {
      const normalizedGiftName = String(giftName || '').toLowerCase();
      const compactGiftName = normalizedGiftName.replace(/[^a-z0-9]/g, '');

      return normalizedGiftName.includes('perfume') ||
        normalizedGiftName.includes('tiny dyny') ||
        normalizedGiftName.includes('tiny diny') ||
        compactGiftName.includes('tinydyny') ||
        compactGiftName.includes('tinydiny');
    }

    it('deve detectar "Perfume"', () => {
      expect(isTargetGift('Perfume')).toBe(true);
    });

    it('deve detectar "perfume" (lowercase)', () => {
      expect(isTargetGift('perfume')).toBe(true);
    });

    it('deve detectar "Tiny Dyny"', () => {
      expect(isTargetGift('Tiny Dyny')).toBe(true);
    });

    it('deve detectar "tiny dyny" (lowercase)', () => {
      expect(isTargetGift('tiny dyny')).toBe(true);
    });

    it('deve detectar "Tiny Diny" (variação)', () => {
      expect(isTargetGift('Tiny Diny')).toBe(true);
    });

    it('deve detectar "tinydyny" (sem espaço)', () => {
      expect(isTargetGift('tinydyny')).toBe(true);
    });

    it('não deve detectar "Rose"', () => {
      expect(isTargetGift('Rose')).toBe(false);
    });

    it('não deve detectar "Heart"', () => {
      expect(isTargetGift('Heart')).toBe(false);
    });

    it('deve lidar com null', () => {
      expect(isTargetGift(null)).toBe(false);
    });

    it('deve lidar com undefined', () => {
      expect(isTargetGift(undefined)).toBe(false);
    });

    it('deve lidar com string vazia', () => {
      expect(isTargetGift('')).toBe(false);
    });
  });

  describe('getGiftTypeFromPayload', () => {
    function getGiftTypeFromPayload(data) {
      return data.giftType ?? data.giftDetails?.giftType;
    }

    it('deve retornar giftType direto', () => {
      expect(getGiftTypeFromPayload({ giftType: 1 })).toBe(1);
    });

    it('deve retornar giftType de giftDetails', () => {
      expect(getGiftTypeFromPayload({ giftDetails: { giftType: 2 } })).toBe(2);
    });

    it('deve preferir giftType direto sobre giftDetails', () => {
      expect(getGiftTypeFromPayload({ giftType: 1, giftDetails: { giftType: 2 } })).toBe(1);
    });

    it('deve retornar undefined se não existir', () => {
      expect(getGiftTypeFromPayload({})).toBeUndefined();
    });
  });

  describe('isGiftCountingSettlement', () => {
    function getGiftTypeFromPayload(data) {
      return data.giftType ?? data.giftDetails?.giftType;
    }

    function isGiftCountingSettlement(data) {
      const giftType = getGiftTypeFromPayload(data);
      if (Number(giftType) === 1 && data.repeatEnd === false) {
        return false;
      }
      return true;
    }

    it('deve retornar false para streak em andamento (giftType 1, repeatEnd false)', () => {
      expect(isGiftCountingSettlement({ giftType: 1, repeatEnd: false })).toBe(false);
    });

    it('deve retornar true para streak finalizado (giftType 1, repeatEnd true)', () => {
      expect(isGiftCountingSettlement({ giftType: 1, repeatEnd: true })).toBe(true);
    });

    it('deve retornar true para gift normal (giftType diferente de 1)', () => {
      expect(isGiftCountingSettlement({ giftType: 2 })).toBe(true);
    });

    it('deve retornar true quando giftType não existe', () => {
      expect(isGiftCountingSettlement({})).toBe(true);
    });
  });

  describe('getGiftRepeatCount', () => {
    function getGiftRepeatCount(data) {
      const rc = Number(data.repeatCount);
      return Number.isFinite(rc) && rc > 0 ? rc : 1;
    }

    it('deve retornar repeatCount quando válido', () => {
      expect(getGiftRepeatCount({ repeatCount: 5 })).toBe(5);
    });

    it('deve retornar 1 quando repeatCount é 0', () => {
      expect(getGiftRepeatCount({ repeatCount: 0 })).toBe(1);
    });

    it('deve retornar 1 quando repeatCount é negativo', () => {
      expect(getGiftRepeatCount({ repeatCount: -1 })).toBe(1);
    });

    it('deve retornar 1 quando repeatCount não existe', () => {
      expect(getGiftRepeatCount({})).toBe(1);
    });

    it('deve retornar 1 quando repeatCount é NaN', () => {
      expect(getGiftRepeatCount({ repeatCount: 'abc' })).toBe(1);
    });
  });

  describe('getUserFromObject', () => {
    function getUserFromObject(data) {
      if (!data) return { uniqueId: null, nickname: null };

      const user = data.user || data.member || data.sender || data.author || data.owner || {};
      const uniqueId = data.uniqueId || user.uniqueId || user.secUid || user.id || null;
      const nickname = data.nickname || user.nickname || user.displayName || uniqueId || null;

      return { uniqueId, nickname };
    }

    it('deve extrair uniqueId e nickname diretos', () => {
      const result = getUserFromObject({ uniqueId: 'user1', nickname: 'User One' });
      expect(result.uniqueId).toBe('user1');
      expect(result.nickname).toBe('User One');
    });

    it('deve extrair de user aninhado', () => {
      const result = getUserFromObject({ user: { uniqueId: 'user2', nickname: 'User Two' } });
      expect(result.uniqueId).toBe('user2');
      expect(result.nickname).toBe('User Two');
    });

    it('deve extrair de member aninhado', () => {
      const result = getUserFromObject({ member: { uniqueId: 'user3', nickname: 'User Three' } });
      expect(result.uniqueId).toBe('user3');
      expect(result.nickname).toBe('User Three');
    });

    it('deve usar uniqueId como fallback para nickname', () => {
      const result = getUserFromObject({ uniqueId: 'user4' });
      expect(result.uniqueId).toBe('user4');
      expect(result.nickname).toBe('user4');
    });

    it('deve retornar nulls para null', () => {
      const result = getUserFromObject(null);
      expect(result.uniqueId).toBeNull();
      expect(result.nickname).toBeNull();
    });

    it('deve retornar nulls para undefined', () => {
      const result = getUserFromObject(undefined);
      expect(result.uniqueId).toBeNull();
      expect(result.nickname).toBeNull();
    });

    it('deve retornar nulls para objeto vazio', () => {
      const result = getUserFromObject({});
      expect(result.uniqueId).toBeNull();
      expect(result.nickname).toBeNull();
    });
  });

  describe('textFromDisplayText', () => {
    function textFromDisplayText(displayText) {
      if (!displayText) return null;
      if (typeof displayText === 'string') return displayText;
      if (displayText.defaultPattern) return displayText.defaultPattern;
      if (displayText.format) return displayText.format;
      if (displayText.displayText) return textFromDisplayText(displayText.displayText);

      const pieces = displayText.pieces || displayText.piecesList;
      if (Array.isArray(pieces)) {
        const text = pieces
          .map(piece => piece.stringValue || piece.text || piece.userValue?.nickname || piece.userValue?.uniqueId || '')
          .join('')
          .trim();
        return text || null;
      }

      return null;
    }

    it('deve retornar string direta', () => {
      expect(textFromDisplayText('hello')).toBe('hello');
    });

    it('deve retornar defaultPattern', () => {
      expect(textFromDisplayText({ defaultPattern: 'pattern' })).toBe('pattern');
    });

    it('deve retornar format', () => {
      expect(textFromDisplayText({ format: 'format' })).toBe('format');
    });

    it('deve recursar em displayText aninhado', () => {
      expect(textFromDisplayText({ displayText: 'nested' })).toBe('nested');
    });

    it('deve concatenar pieces', () => {
      const result = textFromDisplayText({
        pieces: [
          { stringValue: 'hello' },
          { stringValue: ' ' },
          { stringValue: 'world' }
        ]
      });
      expect(result).toBe('hello world');
    });

    it('deve usar piecesList como alternativa', () => {
      const result = textFromDisplayText({
        piecesList: [
          { text: 'foo' },
          { text: 'bar' }
        ]
      });
      expect(result).toBe('foobar');
    });

    it('deve retornar null para null', () => {
      expect(textFromDisplayText(null)).toBeNull();
    });

    it('deve retornar null para undefined', () => {
      expect(textFromDisplayText(undefined)).toBeNull();
    });

    it('deve retornar null para objeto vazio', () => {
      expect(textFromDisplayText({})).toBeNull();
    });
  });

  describe('repeatSequenceKey', () => {
    function repeatSequenceKey(senderKey, commentLower) {
      return JSON.stringify([senderKey, commentLower]);
    }

    it('deve criar chave única para sender e comentário', () => {
      const key = repeatSequenceKey('user1', 'hello');
      expect(key).toBe('["user1","hello"]');
    });

    it('deve diferenciar usuários diferentes', () => {
      const key1 = repeatSequenceKey('user1', 'hello');
      const key2 = repeatSequenceKey('user2', 'hello');
      expect(key1).not.toBe(key2);
    });

    it('deve diferenciar comentários diferentes', () => {
      const key1 = repeatSequenceKey('user1', 'hello');
      const key2 = repeatSequenceKey('user1', 'world');
      expect(key1).not.toBe(key2);
    });
  });
});
