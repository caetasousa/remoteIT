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
		if m := f.minRTT.Load(); m == 0 || r < m {
			f.minRTT.Store(r)
		}
	}
}

// DropInflight considera confirmados todos os quadros enviados. Usado quando
// o navegador avisa que descartou o que estava em trânsito (pedido de
// keyframe).
func (f *FlowControl) DropInflight() { f.acked.Store(f.seq.Load()) }

// Congested informa se o próximo quadro deve esperar: há [MaxInflight]
// quadros em trânsito ou o mais antigo está demorando mais que 3x o menor RTT
// visto (mínimo 50 ms). Um quadro sem ACK há mais de 3 s é dado como perdido.
func (f *FlowControl) Congested(now time.Time) bool {
	sent, acked := f.seq.Load(), f.acked.Load()
	inflight := sent - acked
	if inflight == 0 {
		return false
	}
	oldest := time.Duration(now.UnixNano() - f.sentAt[(acked+1)%256].Load())
	limit := max(50*time.Millisecond, 3*time.Duration(f.minRTT.Load()))
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

// ResetMinRTT esquece o menor RTT visto, para acompanhar mudanças de rede.
func (f *FlowControl) ResetMinRTT() { f.minRTT.Store(f.rtt.Load()) }
