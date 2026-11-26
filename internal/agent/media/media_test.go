package media

import (
	"testing"
	"time"
)

func TestRateControl(t *testing.T) {
	p := Preset{MinKbps: 1000, StartKbps: 4000, MaxKbps: 5000}
	cases := []struct {
		name string
		kbps int
		sec  Second
		want int
	}{
		{"esperas por ACK cortam 25%", 4000, Second{SentKbps: 4000, Frames: 60, Waits: CongestedThreshold}, 3000},
		{"fila alta corta 25%", 4000, Second{SentKbps: 4000, Frames: 60, Queue: QueueHigh + time.Millisecond}, 3000},
		{"o corte respeita o mínimo", 1100, Second{Waits: CongestedThreshold + 5}, 1000},
		{"soluço isolado segura", 2000, Second{SentKbps: 2000, Frames: 60, Waits: CongestedThreshold - 1}, 2000},
		{"fila começando segura", 2000, Second{SentKbps: 2000, Frames: 60, Queue: QueueLow + time.Millisecond}, 2000},
		{"banda usada sem fila sobe 20% + 50", 2000, Second{SentKbps: 1900, Frames: 60}, 2450},
		{"a subida respeita o máximo", 4900, Second{SentKbps: 4900, Frames: 60}, 5000},
		// tela quase parada: o encoder manda bem menos que o alvo, e subir
		// agora faria a próxima rolagem sair com o bitrate máximo
		{"banda ociosa não sobe", 2000, Second{SentKbps: 300, Frames: 60}, 2000},
		{"sem quadros segura", 2000, Second{}, 2000},
	}
	for _, c := range cases {
		var r RateControl
		r.Reset(p)
		r.kbps = c.kbps
		if got := r.Next(c.sec); got != c.want {
			t.Errorf("%s: Next = %d, quero %d", c.name, got, c.want)
		}
	}
	var r RateControl
	if r.Reset(p); r.Kbps() != p.StartKbps {
		t.Errorf("Reset deveria voltar ao bitrate inicial do preset: %d", r.Kbps())
	}
}

func TestFlowControlLimitsInflight(t *testing.T) {
	var f FlowControl
	t0 := time.Unix(1000, 0)
	if f.Congested(t0) {
		t.Fatal("sem quadros em trânsito não pode estar congestionado")
	}
	s1 := f.Sent(t0)
	f.Sent(t0)
	if !f.Congested(t0.Add(time.Millisecond)) {
		t.Fatal("com MinInflight quadros sem ACK e RTT desconhecido deveria esperar")
	}
	f.OnAck(s1, t0.Add(10*time.Millisecond))
	if f.Congested(t0.Add(11 * time.Millisecond)) {
		t.Fatal("após ACK deveria liberar")
	}
	if f.RTT() != 10*time.Millisecond {
		t.Errorf("RTT = %v, want 10ms", f.RTT())
	}
}

func TestFlowControlSlowFrame(t *testing.T) {
	var f FlowControl
	t0 := time.Unix(1000, 0)
	f.OnAck(f.Sent(t0), t0.Add(20*time.Millisecond)) // menor RTT = 20 ms, limite = 20 + 50 ms
	t1 := t0.Add(time.Second)
	f.Sent(t1)
	if f.Congested(t1.Add(40 * time.Millisecond)) {
		t.Error("um quadro dentro do limite não deveria esperar")
	}
	if !f.Congested(t1.Add(75 * time.Millisecond)) {
		t.Error("quadro acima do menor RTT + QueueBudget deveria esperar")
	}
}

func TestFlowControlLostAck(t *testing.T) {
	var f FlowControl
	t0 := time.Unix(1000, 0)
	f.Sent(t0)
	f.Sent(t0)
	if f.Congested(t0.Add(4 * time.Second)) {
		t.Error("ACK perdido há mais de 3 s deveria destravar")
	}
	if f.Congested(t0.Add(4 * time.Second)) {
		t.Error("depois de destravar não há quadros em trânsito")
	}
}

func TestFlowControlIgnoresBogusAcks(t *testing.T) {
	var f FlowControl
	t0 := time.Unix(1000, 0)
	f.Sent(t0)
	f.OnAck(99, t0) // quadro nunca enviado
	f.Sent(t0)
	if !f.Congested(t0) {
		t.Error("ACK de quadro inexistente não pode liberar o fluxo")
	}
}

func TestDropInflight(t *testing.T) {
	var f FlowControl
	t0 := time.Unix(1000, 0)
	f.Sent(t0)
	f.Sent(t0)
	f.DropInflight()
	if f.Congested(t0) {
		t.Error("DropInflight deveria zerar os quadros em trânsito")
	}
}

// A janela cresce com o RTT: com 140 ms e 60 fps, 2 quadros a caminho dariam
// ~14 fps; a janela vai ao máximo. Em LAN fica no mínimo.
func TestFlowControlWindowFollowsRTT(t *testing.T) {
	cases := []struct {
		rtt, interval time.Duration
		want          uint32
	}{
		{0, 16 * time.Millisecond, MinInflight},                    // RTT ainda não medido
		{2 * time.Millisecond, 16 * time.Millisecond, MinInflight}, // LAN
		{50 * time.Millisecond, 16 * time.Millisecond, 5},          // 4 quadros cabem em 50 ms, mais 1
		{140 * time.Millisecond, 16 * time.Millisecond, MaxInflight},
		{140 * time.Millisecond, 0, MinInflight}, // sem preset ainda
	}
	for _, c := range cases {
		var f FlowControl
		t0 := time.Unix(1000, 0)
		f.SetInterval(c.interval)
		if c.rtt > 0 {
			f.OnAck(f.Sent(t0), t0.Add(c.rtt))
		}
		if got := f.Window(); got != c.want {
			t.Errorf("RTT %v, intervalo %v: janela %d, want %d", c.rtt, c.interval, got, c.want)
		}
	}
}

// Com RTT alto, a janela maior deixa passar mais quadros sem ACK, mas um
// quadro velho demais (fila na rede) ainda segura o envio.
func TestFlowControlWideWindowStillStopsOnQueue(t *testing.T) {
	var f FlowControl
	t0 := time.Unix(1000, 0)
	f.SetInterval(16 * time.Millisecond)
	f.OnAck(f.Sent(t0), t0.Add(140*time.Millisecond)) // menor RTT 140 ms
	t1 := t0.Add(time.Second)
	for i := range 5 {
		f.Sent(t1.Add(time.Duration(i) * 16 * time.Millisecond))
	}
	if f.Congested(t1.Add(100 * time.Millisecond)) {
		t.Fatal("5 quadros a caminho com janela 8 e o mais velho dentro do limite: deveria seguir")
	}
	// o limite é o RTT mínimo + QueueBudget (190 ms), não mais 3x (420 ms):
	// com o relay atrás de um túnel, isso deixava a fila crescer demais
	if !f.Congested(t1.Add(200 * time.Millisecond)) {
		t.Fatal("quadro sem ACK há mais que o menor RTT + QueueBudget: deveria esperar")
	}
}
