package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"remoteit/internal/protocol"
	"remoteit/internal/viewer/client"
)

func TestRowsOrderFilterAndDetail(t *testing.T) {
	list := []client.Machine{
		{ID: "1", Name: "zeta", Online: false, Owner: "ana"},
		{ID: "2", Name: "Beta", Online: true, User: "joao", Version: "2026.11.25.1252", Owner: "bia"},
		{ID: "3", Name: "alfa", Online: true, ViewedBy: "bia", Owner: "ana"},
	}
	admin := client.User{Username: "ana", Role: "admin"}
	rows := Rows(list, "", admin)
	if got := []string{rows[0].Title, rows[1].Title, rows[2].Title}; got[0] != "alfa" || got[1] != "Beta" || got[2] != "zeta" {
		t.Fatalf("ordem = %v, want online por nome e offline no fim", got)
	}
	if rows[0].State != InUse || rows[0].Detail != "em uso por bia" {
		t.Errorf("em uso: %+v", rows[0])
	}
	if rows[1].Detail != "online · usuário joao · de bia · v2026.11.25.1252" {
		t.Errorf("detalhe = %q", rows[1].Detail)
	}
	// técnico comum não vê "de <dono>"
	if r := Rows(list, "", client.User{Username: "bia"}); strings.Contains(r[1].Detail, "de bia") {
		t.Errorf("técnico viu o dono: %q", r[1].Detail)
	}
	// filtro por nome, dono ou usuário, sem diferenciar maiúsculas
	for filter, want := range map[string]int{"BET": 1, "joao": 1, "ana": 2, "nada": 0, "  ": 3} {
		if n := len(Rows(list, filter, admin)); n != want {
			t.Errorf("filtro %q: %d linhas, want %d", filter, n, want)
		}
	}
}

func TestSummary(t *testing.T) {
	if s := Summary([]client.Machine{{Online: true}}); s != "1 máquina · 1 online" {
		t.Error(s)
	}
	if s := Summary([]client.Machine{{Online: true}, {}}); s != "2 máquinas · 1 online" {
		t.Error(s)
	}
}

func TestTelemetry(t *testing.T) {
	st := protocol.Stats{Encoder: "H.264 4:4:4 GPU: NVIDIA NVENC", W: 1920, H: 1080, FPS: 60, Kbps: 12400, RTTms: 132,
		RTTMin: 45, EncodeUs: 4200}
	seg := Telemetry(st, 57, Local{GPUDecode: true, GPUDraw: true, DecodeUs: 1800, Route: "local"})
	want := []Segment{
		{"Codec", "H.264 4:4:4 GPU", Accent}, {"Decodifica", "GPU", Normal}, {"Resolução", "1920×1080", Normal},
		{"FPS", "57", Good}, {"Banda", "12.4 Mbps", Normal}, {"RTT", "132 ms", Warn},
		{"Fila", "87 ms", Bad}, {"Codif.", "4,2 ms", Normal}, {"Decod.", "1,8 ms", Normal}, {"Rota", "local", Good},
	}
	if len(seg) != len(want) {
		t.Fatalf("segmentos = %+v", seg)
	}
	for i := range want {
		if seg[i] != want[i] {
			t.Errorf("segmento %d = %+v, want %+v", i, seg[i], want[i])
		}
	}
	if s := Telemetry(protocol.Stats{Encoder: "JPEG"}, 0, Local{}); s[0].Value != "JPEG" || s[0].Level != Normal || s[3].Level != Muted {
		t.Errorf("JPEG: %+v", s)
	}
	// agente antigo (sem o RTT mínimo): sem o segmento da fila
	for _, s := range Telemetry(protocol.Stats{Encoder: "H.264 GPU: x", RTTms: 100}, 30, Local{}) {
		if s.Label == "Fila" || s.Label == "Codif." || s.Label == "Rota" {
			t.Errorf("segmento %q sem dado para ele", s.Label)
		}
	}
}

func TestLevels(t *testing.T) {
	for ms, want := range map[int64]Level{0: Good, 30: Good, 60: Good, 140: Warn, 300: Bad} {
		if got := RTTLevel(ms); got != want {
			t.Errorf("RTTLevel(%d) = %v, want %v", ms, got, want)
		}
	}
	for fps, want := range map[int]Level{0: Muted, 14: Warn, 30: Normal, 60: Good} {
		if got := FPSLevel(fps); got != want {
			t.Errorf("FPSLevel(%d) = %v, want %v", fps, got, want)
		}
	}
}

func TestColorHint(t *testing.T) {
	cases := []struct {
		want, h264, broken bool
		preset, enc, hint  string
	}{
		{true, true, false, "quality", "H.264 4:4:4 GPU: x", ""},
		{false, true, false, "quality", "H.264 GPU: x", ""},
		{true, true, false, "quality", "", ""}, // sem vídeo ainda
		{true, false, false, "quality", "JPEG", "4:4:4 indisponível: este Windows não decodifica H.264"},
		{true, true, true, "quality", "H.264 GPU: x", "4:4:4 desligado: falhou ao decodificar aqui"},
		{true, true, false, "balanced", "H.264 GPU: x", "4:4:4 só em Nítido ou Máximo"},
		{true, true, false, "quality60", "H.264 GPU: x", "4:4:4 indisponível no PC remoto (veja o agent.log)"},
	}
	for _, c := range cases {
		if got := ColorHint(c.want, c.h264, c.broken, c.preset, c.enc); got != c.hint {
			t.Errorf("ColorHint(%+v) = %q", c, got)
		}
	}
}

func TestDiagnostics(t *testing.T) {
	d := Diagnostics("2026.11.25.1252", true, false, []string{"avc444", "h264", "jpeg"},
		protocol.Stats{Encoder: "H.264 GPU: Intel QSV", W: 1280, H: 720, FPS: 60, Kbps: 8000, Target: 12000, RTTms: 52, RTTMin: 48, EncodeUs: 3500},
		58, "quality", Local{GPUDraw: true, DecodeUs: 2100, Route: "local"})
	for _, want := range []string{"2026.11.25.1252", "H.264 sim · 4:4:4 sim · AV1 não", "Extensão de Vídeo AV1",
		"avc444, h264, jpeg", "Em uso: H.264 GPU", "Encoder no PC remoto: Intel QSV", "58 fps desenhados (60 enviados)", "Nítido (nativa 60fps)",
		"(alvo 12.0)", "Desenho: GPU (Direct3D 11)", "Rota: direta, no PC do relay", "sem fila 48 ms · fila 4 ms",
		"Captura + codificação no PC remoto: 3,5 ms", "Decodificação + desenho aqui: 2,1 ms"} {
		if !strings.Contains(d, want) {
			t.Errorf("diagnóstico sem %q:\n%s", want, d)
		}
	}
}

func TestLoginError(t *testing.T) {
	dns := &url.Error{Op: "Post", URL: "https://x", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Name: "suporte.exemplo", Err: "no such host"}}}
	refused := &url.Error{Op: "Post", URL: "https://x", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}
	cases := []struct {
		err  error
		form bool
		want string
	}{
		{nil, true, ""},
		{client.ErrUnauthorized, true, "Usuário ou senha incorretos."},
		{client.ErrUnauthorized, false, "Sua sessão expirou. Entre de novo."},
		{dns, true, "Endereço do relay não encontrado (suporte.exemplo). Confira o endereço."},
		{refused, true, "O relay não respondeu. Confira a internet e o endereço."},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), true, "O relay não respondeu. Confira a internet e o endereço."},
		{errors.New("muitas tentativas; espere 1 minuto"), true, "Muitas tentativas; espere 1 minuto."},
		{errors.New("usuário ou senha inválidos"), true, "Usuário ou senha inválidos."},
	}
	for _, c := range cases {
		if got := LoginError(c.err, c.form); got != c.want {
			t.Errorf("LoginError(%v, %v) = %q, want %q", c.err, c.form, got, c.want)
		}
	}
}

func TestSmallTexts(t *testing.T) {
	if RTTText(0) != "<1 ms" || RTTText(132) != "132 ms" {
		t.Error(RTTText(0), RTTText(132))
	}
	if PresetLabel("turbo") != "Ultra-rápido (1280p 60fps)" || PresetLabel("x") != "x" {
		t.Error(PresetLabel("turbo"))
	}
	if d := Diagnostics("", false, false, []string{"jpeg"}, protocol.Stats{Encoder: "JPEG (CPU)"}, 1, "quality", Local{}); !strings.Contains(d, "Encoder no PC remoto: JPEG (CPU)") || !strings.Contains(d, "Desenho: CPU (GDI)") {
		t.Errorf("JPEG sem encoder:\n%s", d)
	}
}
