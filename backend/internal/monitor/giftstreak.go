package monitor

import (
	"log"
	"strings"
	"sync/atomic"
	"time"
)

var (
	// giftStreakSettleTimeout: a classe nova do conector (TikTokLiveConnection)
	// emite o primeiro evento de um streak com repeatEnd=false; o evento final
	// com repeatEnd=true pode nunca chegar (amostragem do TikTok). Sem fallback,
	// o presente se perde (perfil mostra 0 presentes e o presente-alvo não é
	// registrado). Se nenhum evento do mesmo streak chegar nesse prazo, o
	// monitor liquida o streak com a última contagem conhecida.
	giftStreakSettleTimeout = atomic.Int64{}
	// giftStreakKeepAfterSettle: janela para suprimir eventos finais atrasados
	// que cheguem depois da liquidação por timeout.
	giftStreakKeepAfterSettle = atomic.Int64{}
)

func init() {
	giftStreakSettleTimeout.Store((20 * time.Second).Nanoseconds())
	giftStreakKeepAfterSettle.Store((90 * time.Second).Nanoseconds())
}

type giftStreak struct {
	data           EventData
	lastCount      int
	settledEmitted bool
	timer          *time.Timer
}

func giftStreakKey(data EventData) string {
	user := strings.ToLower(asString(data["uniqueId"]))
	id := asString(data["giftId"])
	if id == "" {
		id = strings.ToLower(asString(data["giftName"]))
	}
	return user + "|" + id
}

// handleGiftReceived processa "any-gift-received". Eventos liquidados
// (repeatEnd=true) seguem direto; eventos intermediários de streak ficam
// pendentes e são liquidados pelo evento final ou pelo timeout.
func (m *Monitor) handleGiftReceived(data EventData) {
	count, _ := toInt(data["repeatCount"])
	if count < 1 {
		count = 1
	}
	key := giftStreakKey(data)

	m.mu.Lock()
	st := m.giftStreaks[key]
	if truthy(data["repeatEnd"]) {
		if st != nil {
			if st.settledEmitted {
				// Final chegou depois da liquidação por timeout: suprimir
				// para não gravar o mesmo streak duas vezes. A entrada fica
				// no mapa até o fim da janela de tolerância, para suprimir
				// também o "new-gift-user" final atrasado.
				m.mu.Unlock()
				return
			}
			if st.timer != nil {
				st.timer.Stop()
			}
			delete(m.giftStreaks, key)
		}
		m.mu.Unlock()
		m.emit(EventAnyGift, data)
		return
	}
	if st != nil && st.settledEmitted {
		m.mu.Unlock()
		return
	}
	if st == nil {
		st = &giftStreak{}
		m.giftStreaks[key] = st
	}
	if count > st.lastCount {
		st.lastCount = count
	}
	st.data = data
	if st.timer != nil {
		st.timer.Stop()
	}
	st.timer = time.AfterFunc(time.Duration(giftStreakSettleTimeout.Load()), func() { m.settleGiftStreak(key) })
	m.mu.Unlock()
}

// settleGiftStreak liquida um streak órfão: emite o evento como se tivesse
// chegado o repeatEnd=true, salvando o presente e acionando o fluxo de
// presente-alvo/correlação.
func (m *Monitor) settleGiftStreak(key string) {
	m.mu.Lock()
	st := m.giftStreaks[key]
	if st == nil || st.settledEmitted {
		m.mu.Unlock()
		return
	}
	st.settledEmitted = true
	st.timer = nil
	data := st.data
	data["repeatEnd"] = true
	data["repeatCount"] = st.lastCount
	m.mu.Unlock()

	log.Printf("[Monitor] Streak de presente liquidado por timeout: user=%v gift=%v count=%v", data["uniqueId"], data["giftName"], data["repeatCount"])
	m.emit(EventAnyGift, data)
	m.handleTargetGift(data)

	time.AfterFunc(time.Duration(giftStreakKeepAfterSettle.Load()), func() {
		m.mu.Lock()
		if cur := m.giftStreaks[key]; cur == st {
			delete(m.giftStreaks, key)
		}
		m.mu.Unlock()
	})
}

// handleSettledGiftUser processa "new-gift-user" (evento final do bridge).
// Se o streak já foi liquidado por timeout, o fluxo de presente-alvo já
// rodou e o evento é suprimido.
func (m *Monitor) handleSettledGiftUser(data EventData) {
	key := giftStreakKey(data)
	m.mu.Lock()
	st := m.giftStreaks[key]
	settled := st != nil && st.settledEmitted
	m.mu.Unlock()
	if settled {
		return
	}
	m.handleTargetGift(data)
}

func (m *Monitor) isGiftCountingSettlement(data EventData) bool {
	giftType, _ := toInt(data["giftType"])
	if giftType != 1 {
		return true
	}
	if _, ok := data["repeatEnd"]; !ok {
		return true
	}
	return truthy(data["repeatEnd"])
}
