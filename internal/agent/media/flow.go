package media

import (
	"sync/atomic"
	"time"
)

// Quadros enviados e ainda não confirmados. Com no máximo N a caminho, a
// transmissão não passa de N/RTT quadros por segundo: com 2 e 140 ms de RTT
// (relay atrás de um túnel numa internet doméstica), seriam ~14 fps. Por isso
// a janela cresce com o RTT, de MinInflight até MaxInflight (veja
// [FlowControl.Window]). Mais quadros a caminho não aumentam o atraso enquanto
// a banda dá conta: eles só deixam de esperar a confirmação do anterior.
// Quando a banda não dá conta, a fila aparece como quadro velho sem ACK, e
// [FlowControl.Congested] segura o envio.
const (
	MinInflight = 2 // em LAN, e enquanto o RTT ainda não foi medido
	MaxInflight = 8 // ~57 fps com 140 ms de RTT
)

// lostAckAfter é quanto tempo sem ACK faz o agente supor que o ACK se perdeu
// e destravar o envio.
const lostAckAfter = 3 * time.Second

// QueueBudget é quanto um quadro pode esperar em fila, além do RTT mínimo,
// antes de o envio parar. É um valor fixo de propósito: um limite
// proporcional ao RTT (o antigo 3x) deixava a fila chegar a ~420 ms com o
// relay atrás de um túnel (RTT de 140 ms), e cada um desses ms é atraso na
// tela do técnico.
const QueueBudget = 50 * time.Millisecond

// FlowControl implementa o controle de fluxo fim a fim: o navegador confirma
// (ack) cada quadro, e o agente não codifica um novo enquanto houver quadros
// demais em trânsito. Isso evita fila nos buffers TCP, a principal causa de
// atraso em acesso remoto sobre TCP.
//
// OnAck e DropInflight são chamados pela goroutine de leitura; os demais
// métodos, pelo loop de stream. Por isso o estado é atômico.
type FlowControl struct {
	seq      atomic.Uint32
	acked    atomic.Uint32
	rtt      atomic.Int64
	minRTT   atomic.Int64
	interval atomic.Int64 // intervalo entre quadros do preset (veja SetInterval)
	sentAt   [256]atomic.Int64
	rttSum   atomic.Int64 // soma e contagem dos RTT desde o último TakeRTT
	rttN     atomic.Int64
	winMin   atomic.Int64 // menor RTT desde o último ResetMinRTT
}

// Sent registra o envio de um novo quadro em at e devolve seu número de
// sequência.
func (f *FlowControl) Sent(at time.Time) uint32 {
	seq := f.seq.Add(1)
	f.sentAt[seq%256].Store(at.UnixNano())
	return seq
}

// OnAck processa a confirmação do quadro seq. ACKs antigos, repetidos ou de
// quadros que ainda não foram enviados são ignorados.
func (f *FlowControl) OnAck(seq uint32, now time.Time) {
	for {
		cur := f.acked.Load()
		if int32(seq-cur) <= 0 || int32(f.seq.Load()-seq) < 0 {
			return
		}
		if f.acked.CompareAndSwap(cur, seq) {
			break
		}
	}
	if t := f.sentAt[seq%256].Load(); t != 0 {
		r := now.UnixNano() - t
		f.rtt.Store(r)
		f.rttSum.Add(r)
		f.rttN.Add(1)
		if m := f.minRTT.Load(); m == 0 || r < m {
			f.minRTT.Store(r)
		}
		if m := f.winMin.Load(); m == 0 || r < m {
			f.winMin.Store(r)
		}
	}
}

// DropInflight considera confirmados todos os quadros enviados. Usado quando
// o navegador avisa que descartou o que estava em trânsito (pedido de
// keyframe).
func (f *FlowControl) DropInflight() { f.acked.Store(f.seq.Load()) }

// Congested informa se o próximo quadro deve esperar: a janela (veja
// [FlowControl.Window]) está cheia ou o quadro mais antigo está esperando há
// mais que o menor RTT visto mais [QueueBudget] (mínimo 50 ms). Um quadro
// sem ACK há mais de 3 s é dado como perdido.
func (f *FlowControl) Congested(now time.Time) bool {
	sent, acked := f.seq.Load(), f.acked.Load()
	inflight := sent - acked
	if inflight == 0 {
		return false
	}
	oldest := time.Duration(now.UnixNano() - f.sentAt[(acked+1)%256].Load())
	limit := max(50*time.Millisecond, time.Duration(f.minRTT.Load())+QueueBudget)
	switch {
	case oldest > lostAckAfter:
		f.acked.Store(sent)
		return false
	case inflight >= f.Window() || oldest > limit:
		return true
	}
	return false
}

// SetInterval informa o intervalo entre quadros do preset atual (1/FPS), que
// define quantos quadros cabem num RTT.
func (f *FlowControl) SetInterval(d time.Duration) { f.interval.Store(int64(d)) }

// Window é quantos quadros podem estar a caminho: os que cabem no menor RTT
// visto, mais um, entre MinInflight e MaxInflight. Usa o menor RTT, não o
// último, para a própria fila não aumentar a janela.
func (f *FlowControl) Window() uint32 {
	rtt, iv := f.minRTT.Load(), f.interval.Load()
	if rtt <= 0 || iv <= 0 {
		return MinInflight
	}
	n := uint32((rtt+iv-1)/iv) + 1
	return min(max(n, MinInflight), MaxInflight)
}

// RTT devolve o tempo de ida e volta do último quadro confirmado.
func (f *FlowControl) RTT() time.Duration { return time.Duration(f.rtt.Load()) }

// MinRTT devolve o menor RTT recente (a rede sem fila).
func (f *FlowControl) MinRTT() time.Duration { return time.Duration(f.minRTT.Load()) }

// TakeRTT devolve o RTT médio dos quadros confirmados desde a última chamada
// (0 se nenhum foi confirmado) e recomeça a média.
func (f *FlowControl) TakeRTT() time.Duration {
	n := f.rttN.Swap(0)
	s := f.rttSum.Swap(0)
	if n == 0 {
		return 0
	}
	return time.Duration(s / n)
}

// ResetMinRTT passa a usar o menor RTT do período desde a última chamada,
// para acompanhar mudanças de rede (um caminho novo mais lento) sem adotar
// uma amostra inflada pela própria fila.
func (f *FlowControl) ResetMinRTT() {
	if m := f.winMin.Swap(0); m > 0 {
		f.minRTT.Store(m)
	}
}
