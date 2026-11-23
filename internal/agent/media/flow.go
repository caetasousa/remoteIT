package media

import (
	"sync/atomic"
	"time"
)

// MaxInflight é o máximo de quadros enviados e ainda não confirmados. Em LAN,
// 2 bastam; menos quadros em trânsito significa menos latência percebida.
const MaxInflight = 2

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
	seq    atomic.Uint32
	acked  atomic.Uint32
	rtt    atomic.Int64
	minRTT atomic.Int64
	sentAt [256]atomic.Int64
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
	case inflight >= MaxInflight || oldest > limit:
		return true
	}
	return false
}

// RTT devolve o tempo de ida e volta do último quadro confirmado.
func (f *FlowControl) RTT() time.Duration { return time.Duration(f.rtt.Load()) }

// ResetMinRTT esquece o menor RTT visto, para acompanhar mudanças de rede.
func (f *FlowControl) ResetMinRTT() { f.minRTT.Store(f.rtt.Load()) }
