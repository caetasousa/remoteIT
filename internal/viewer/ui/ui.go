// Package ui decide o que as janelas do visualizador nativo mostram: as
// linhas da lista de máquinas, a barra de telemetria da sessão, o aviso de
// por que a cor 4:4:4 não está ativa e o diagnóstico de vídeo.
//
// É Go puro, sem Windows: internal/viewer/win só desenha o que sai daqui, e
// as regras ficam testadas no Linux.
package ui

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"remoteit/internal/protocol"
	"remoteit/internal/viewer/client"
)

// Level é o tom de uma informação na tela.
type Level int

// Tons, do neutro ao problema.
const (
	Normal Level = iota
	Muted        // secundário
	Accent       // destaque (cor 4:4:4 ativa, máquina online)
	Good
	Warn
	Bad
)

// State é a situação de uma máquina na lista.
type State int

// Situações de uma máquina.
const (
	Offline State = iota
	Online
	InUse // outro técnico (ou você em outra janela) está com a tela aberta
)

// Row é uma linha da lista de máquinas.
type Row struct {
	Machine client.Machine
	State   State
	Title   string // nome da máquina
	Detail  string // segunda linha: situação, dono, versão
}

// Rows ordena e filtra a lista: online primeiro, depois por nome. filter
// procura no nome, no dono e no usuário logado no PC, sem diferenciar
// maiúsculas. O dono só aparece para admins, quando não é o próprio técnico.
func Rows(list []client.Machine, filter string, me client.User) []Row {
	f := strings.ToLower(strings.TrimSpace(filter))
	out := make([]Row, 0, len(list))
	for _, m := range list {
		if f != "" && !strings.Contains(strings.ToLower(m.Name+" "+m.Owner+" "+m.User), f) {
			continue
		}
		r := Row{Machine: m, Title: m.Name, State: Offline}
		var parts []string
		switch {
		case !m.Online:
			parts = append(parts, "offline")
		case m.ViewedBy != "":
			r.State = InUse
			parts = append(parts, "em uso por "+m.ViewedBy)
		default:
			r.State = Online
			parts = append(parts, "online")
		}
		if m.Online && m.User != "" {
			parts = append(parts, "usuário "+m.User)
		}
		if me.Role == "admin" && m.Owner != "" && m.Owner != me.Username {
			parts = append(parts, "de "+m.Owner)
		}
		if m.Version != "" {
			parts = append(parts, "v"+m.Version)
		}
		r.Detail = strings.Join(parts, " · ")
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b Row) int {
		if (a.State == Offline) != (b.State == Offline) {
			if a.State == Offline {
				return 1
			}
			return -1
		}
		return cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
	return out
}

// Summary é a contagem acima da lista ("3 máquinas · 2 online").
func Summary(list []client.Machine) string {
	online := 0
	for _, m := range list {
		if m.Online {
			online++
		}
	}
	noun := "máquinas"
	if len(list) == 1 {
		noun = "máquina"
	}
	return fmt.Sprintf("%d %s · %d online", len(list), noun, online)
}

// Segment é um pedaço da barra de telemetria.
type Segment struct {
	Label string // rótulo pequeno ("RTT")
	Value string // valor ("132 ms")
	Level Level
}

// Codec separa o nome do encoder que o agente manda ("H.264 4:4:4 GPU:
// NVIDIA NVENC") no codec ("H.264 4:4:4 GPU") e no detalhe (o encoder).
func Codec(encoder string) (codec, detail string) {
	c, d, _ := strings.Cut(encoder, ": ") // não em "4:4:4", que não tem espaço
	return strings.TrimSpace(c), strings.TrimSpace(d)
}

// Is444 informa se o encoder em uso é o 4:4:4 (AVC444).
func Is444(encoder string) bool { return strings.Contains(encoder, "4:4:4") }

// RTTLevel dá o tom do RTT: até 60 ms é bom, até 150 ms aceitável.
func RTTLevel(ms int64) Level {
	switch {
	case ms <= 60:
		return Good
	case ms <= 150:
		return Warn
	}
	return Bad
}

// FPSLevel dá o tom dos quadros por segundo desenhados.
func FPSLevel(fps int) Level {
	switch {
	case fps >= 45:
		return Good
	case fps >= 20:
		return Normal
	case fps > 0:
		return Warn
	}
	return Muted
}

// Telemetry monta a barra da sessão a partir do stats do agente (1x/s),
// dos quadros que este PC desenhou no último segundo e de onde ele
// decodifica.
func Telemetry(st protocol.Stats, drawn int, gpuDecode bool) []Segment {
	codec, _ := Codec(st.Encoder)
	if codec == "" {
		codec = "—"
	}
	lvl := Normal
	if Is444(st.Encoder) {
		lvl = Accent
	}
	dec := "CPU"
	if gpuDecode {
		dec = "GPU"
	}
	return []Segment{
		{"Codec", codec, lvl},
		{"Decodifica", dec, Normal},
		{"Resolução", fmt.Sprintf("%d×%d", st.W, st.H), Normal},
		{"FPS", fmt.Sprintf("%d", drawn), FPSLevel(drawn)},
		{"Banda", fmt.Sprintf("%.1f Mbps", float64(st.Kbps)/1000), Normal},
		{"RTT", RTTText(st.RTTms), RTTLevel(st.RTTms)},
	}
}

// NativePreset informa se o preset é de resolução nativa (onde o 4:4:4 vale).
func NativePreset(p string) bool { return p == "quality" || p == "quality60" }

// ColorHint explica por que a cor 4:4:4 pedida não está em uso; "" quando
// está, quando não foi pedida ou quando ainda não há vídeo.
func ColorHint(want, canH264, broken bool, preset, encoder string) string {
	switch {
	case !want || encoder == "" || Is444(encoder):
		return ""
	case !canH264:
		return "4:4:4 indisponível: este Windows não decodifica H.264"
	case broken:
		return "4:4:4 desligado: falhou ao decodificar aqui"
	case !NativePreset(preset):
		return "4:4:4 só em Nítido ou Máximo"
	}
	return "4:4:4 indisponível no PC remoto (veja o agent.log)"
}

// Decoders resume o que este PC decodifica, para o rodapé e o diagnóstico.
func Decoders(h264, av1 bool) string {
	yes := func(b bool) string {
		if b {
			return "sim"
		}
		return "não"
	}
	return "H.264 " + yes(h264) + " · 4:4:4 " + yes(h264) + " · AV1 " + yes(av1)
}

// Diagnostics é o texto de Ajuda ▸ Diagnóstico de vídeo.
func Diagnostics(version string, h264, av1 bool, offered []string, st protocol.Stats, drawn int, preset string) string {
	codec, enc := Codec(st.Encoder)
	if codec == "" {
		codec, enc = "nenhum (sem vídeo ainda)", "—"
	}
	enc = cmp.Or(enc, codec) // JPEG não tem nome de encoder à parte
	var b strings.Builder
	fmt.Fprintf(&b, "Visualizador: %s\n\n", cmp.Or(version, "desenvolvimento"))
	fmt.Fprintf(&b, "Este PC decodifica: %s\n", Decoders(h264, av1))
	if !av1 {
		b.WriteString("  (AV1 precisa da \"Extensão de Vídeo AV1\" da Microsoft Store)\n")
	}
	fmt.Fprintf(&b, "Codecs pedidos ao agente: %s\n\n", strings.Join(offered, ", "))
	fmt.Fprintf(&b, "Em uso: %s\nEncoder no PC remoto: %s\n", codec, enc)
	fmt.Fprintf(&b, "Qualidade: %s\n%d×%d · %d fps desenhados (%d enviados) · %.1f Mbps · RTT %s\n",
		PresetLabel(preset), st.W, st.H, drawn, st.FPS, float64(st.Kbps)/1000, RTTText(st.RTTms))
	return b.String()
}

// LoginError transforma o erro de entrar no relay numa frase para o
// técnico. fromForm diz se veio do formulário (senha errada) ou da sessão
// guardada (sessão expirada). Erros que o relay explica ("muitas
// tentativas…") passam como vieram.
func LoginError(err error, fromForm bool) string {
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var netErr net.Error
	switch {
	case err == nil:
		return ""
	case errors.Is(err, client.ErrUnauthorized) && fromForm:
		return "Usuário ou senha incorretos."
	case errors.Is(err, client.ErrUnauthorized):
		return "Sua sessão expirou. Entre de novo."
	case errors.As(err, &dns):
		return "Endereço do relay não encontrado (" + dns.Name + "). Confira o endereço."
	case errors.As(err, &cert):
		return "O certificado do relay não é válido. Confira o endereço (https://…)."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, syscall.ECONNREFUSED),
		errors.As(err, &netErr):
		return "O relay não respondeu. Confira a internet e o endereço."
	}
	msg := err.Error() // a frase do relay, com maiúscula e ponto como as nossas
	if r, n := utf8.DecodeRuneInString(msg); n > 0 {
		msg = string(unicode.ToUpper(r)) + msg[n:]
	}
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

// PresetLabel é o rótulo do preset como no menu ("Nítido (nativa 60fps)").
func PresetLabel(p string) string {
	for _, x := range client.Presets {
		if x.Value == p {
			return x.Label
		}
	}
	return p
}

// RTTText é o RTT para exibir: abaixo de 1 ms (rede local) vira "<1 ms".
func RTTText(ms int64) string {
	if ms <= 0 {
		return "<1 ms"
	}
	return fmt.Sprintf("%d ms", ms)
}
