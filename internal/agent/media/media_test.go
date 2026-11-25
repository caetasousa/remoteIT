package media

import (
	"testing"
	"time"
)

func TestNextBitrate(t *testing.T) {
	p := Preset{MinKbps: 1000, MaxKbps: 5000}
	cases := []struct {
		name                    string
		kbps, congested, frames int
		want                    int
	}{
		{"congestionado reduz 25%", 4000, CongestedThreshold, 60, 3000},
		{"congestionado respeita o mínimo", 1100, CongestedThreshold + 5, 0, 1000},
		{"soluço isolado mantém", 2000, CongestedThreshold - 1, 60, 2000},
		{"livre sobe 20% + 50", 2000, 0, 60, 2450},
		{"livre respeita o máximo", 4900, 0, 60, 5000},
		{"sem quadros mantém", 2000, 0, 0, 2000},
	}
	for _, c := range cases {
		if got := NextBitrate(c.kbps, p, c.congested, c.frames); got != c.want {
			t.Errorf("%s: NextBitrate = %d, want %d", c.name, got, c.want)
		}
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
		t.Fatal("com MaxInflight quadros sem ACK deveria esperar")
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
	f.OnAck(f.Sent(t0), t0.Add(20*time.Millisecond)) // menor RTT = 20 ms, limite = 60 ms
	t1 := t0.Add(time.Second)
	f.Sent(t1)
	if f.Congested(t1.Add(40 * time.Millisecond)) {
		t.Error("um quadro dentro do limite não deveria esperar")
	}
	if !f.Congested(t1.Add(70 * time.Millisecond)) {
		t.Error("quadro acima de 3x o menor RTT deveria esperar")
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
