// Package media é o pipeline de vídeo do agente: escolhe o encoder, controla
// o fluxo de quadros e adapta o bitrate à rede.
//
// As fontes de vídeo concretas ficam nos subpacotes (capture, jpeg, h264) e
// são injetadas por [Encoders], o que permite testar o pipeline com fontes
// falsas e compilar este pacote em qualquer sistema.
package media

import (
	"errors"
	"image"
	"time"
)

// Flags de [Source.Next].
const (
	FlagKey    = 1 // força keyframe
	FlagRepeat = 2 // re-codifica o último quadro (refina a nitidez com a tela parada)
)

// ErrReopen pede ao pipeline para fechar e reabrir a fonte, por exemplo
// quando a resolução ou o desktop (tela de login, UAC) mudou.
var ErrReopen = errors.New("media: reabrir captura")

// Frame é um quadro codificado.
type Frame struct {
	Codec    byte // protocol.CodecH264 ou protocol.CodecJPEG
	Keyframe bool
	W, H     int
	Data     []byte
}

// Source produz quadros codificados.
type Source interface {
	// Next espera até timeout por mudança na tela. (nil, nil) significa que
	// nada mudou.
	Next(timeout time.Duration, flags int) (*Frame, error)
	SetBitrate(kbps int)
	Name() string
	Close()
}

// CursorReporter é implementado por fontes que sabem a posição do cursor. O
// pipeline a envia ao técnico para ele desenhar o cursor por cima do vídeo.
type CursorReporter interface {
	CursorPos() (x, y int, visible bool)
}

// Still captura a tela inteira sem compressão, para o refresh de nitidez
// (veja [Stream]). [capture.Capturer] já satisfaz esta interface.
type Still interface {
	Capture() (*image.RGBA, error)
	Close()
}

// Encoders são as fábricas de [Source] disponíveis no build.
type Encoders struct {
	// H264 pode devolver erro (sem GPU, sem Media Foundation, build sem CGO);
	// o pipeline cai para JPEG.
	H264 func(maxw, fps, kbps int) (Source, error)
	JPEG func(maxw, kbps int) (Source, error)
	// CursorShape devolve o nome CSS do cursor atual. Opcional.
	CursorShape func() string
	// FollowDesktop reanexa a thread de captura ao desktop do Windows que
	// está recebendo entrada agora (Default ou, na tela de bloqueio/login,
	// Winlogon) — veja internal/agent/desktop. Chamado antes de toda
	// (re)abertura da captura; nil ou erro não impedem a tentativa de abrir,
	// só deixam de garantir que a thread está no desktop certo.
	FollowDesktop func() error
	// Still abre um capturador de tela sem compressão. Opcional: com ele, o
	// pipeline manda uma imagem PNG sem perda quando a tela fica parada,
	// corrigindo o que o vídeo perde (cor em meia resolução do H.264 4:2:0,
	// artefatos de compressão). Chamado na thread de captura, então segue o
	// mesmo desktop que o vídeo.
	Still func() (Still, error)
}

// Sender envia mensagens ao técnico. Implementações devem ser seguras para
// uso concorrente, e SendBinary não pode guardar b depois de retornar: o
// pipeline reutiliza o buffer no quadro seguinte.
type Sender interface {
	SendJSON(v any) error
	SendBinary(b []byte) error
}

// Preset é um perfil de qualidade escolhido pelo técnico.
type Preset struct {
	MaxW    int // 0 = resolução nativa
	FPS     int
	MinKbps int
	MaxKbps int
}

// Presets disponíveis, pelo nome usado no protocolo.
var Presets = map[string]Preset{
	"turbo":     {1280, 60, 4000, 10000}, // latência mínima
	"balanced":  {1920, 60, 8000, 20000}, // 1080p
	"quality":   {0, 60, 15000, 40000},   // nativa, padrão
	"quality60": {0, 60, 25000, 80000},   // máximo, LAN gigabit
}

// DefaultPreset é o preset usado até o técnico escolher outro.
const DefaultPreset = "quality"

// CongestedThreshold é quantas esperas por ACK (de ~3 ms cada) num segundo
// contam como congestionamento de verdade: ~30 ms parados. Abaixo disso é um
// soluço isolado da rede (comum através de túnel), que não justifica derrubar
// a qualidade.
const CongestedThreshold = 10

// NextBitrate calcula o alvo de bitrate do próximo segundo: reduz rápido
// quando houve congestionamento real (-25%) e volta logo quando a rede está
// livre (+20% + 50 kbps); num soluço isolado, mantém. Sempre dentro dos
// limites do preset. Subir 20% por segundo recupera uma queda de 25% em ~2 s;
// com 10% a imagem ficava vários segundos borrada depois de um soluço.
func NextBitrate(kbps int, p Preset, congested, frames int) int {
	switch {
	case congested >= CongestedThreshold:
		return max(p.MinKbps, kbps*3/4)
	case congested > 0:
		return kbps // soluço: segura onde está, sem cortar nem subir
	case frames > 0:
		return min(p.MaxKbps, kbps*6/5+50)
	}
	return kbps
}
