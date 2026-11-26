// Package media é o pipeline de vídeo do agente: escolhe o encoder, controla
// o fluxo de quadros e adapta o bitrate à rede.
//
// As fontes de vídeo concretas ficam nos subpacotes (capture, jpeg, gpu) e
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
	Codec    byte // protocol.Codec*
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

// Params diz como abrir uma fonte de vídeo.
type Params struct {
	// MaxW e MaxH limitam o tamanho do quadro (0 = sem limite naquela
	// direção). A fonte reduz a tela mantendo a proporção, com
	// internal/scale.Fit: é assim que o vídeo sai do tamanho da janela do
	// técnico em vez de mandar pixels que ele não vai ver.
	MaxW, MaxH int
	FPS        int
	Kbps       int
}

// Encoders são as fábricas de [Source] disponíveis no build.
type Encoders struct {
	// H264 pode devolver erro (sem GPU, sem Media Foundation, build sem CGO);
	// o pipeline cai para o próximo codec que o técnico aceita (no fim, JPEG).
	H264 func(Params) (Source, error)
	// AVC444 é cor 4:4:4 em dois fluxos H.264 (veja internal/avc444). Só nos
	// presets sem redução de resolução e só com encoder de GPU. Opcional.
	AVC444 func(Params) (Source, error)
	// AV1 só existe com encoder AV1 de GPU (placas de 2022 em diante).
	// Opcional.
	AV1  func(Params) (Source, error)
	JPEG func(Params) (Source, error)
	// CursorShape devolve o nome CSS do cursor atual. Opcional.
	CursorShape func() string
	// FollowDesktop reanexa a thread de captura ao desktop do Windows que
	// está recebendo entrada agora (Default ou, na tela de bloqueio/login,
	// Winlogon) — veja internal/agent/desktop. Chamado antes de toda
	// (re)abertura da captura; nil ou erro não impedem a tentativa de abrir,
	// só deixam de garantir que a thread está no desktop certo.
	FollowDesktop func() error
	// Still abre um capturador de tela sem compressão, para o refresh sem
	// perda das fontes que não sabem ler a própria imagem ([StillReader]).
	// Opcional; só vale na resolução nativa. Chamado na thread de captura,
	// então segue o mesmo desktop que o vídeo.
	Still func() (Still, error)
}

// TileSource é implementada por fontes que sabem quais partes da tela
// mudaram (o DXGI informa). Com ela, o pipeline manda sem perda só o que
// parou de mudar e diz ao técnico onde cada quadro de vídeo vale (veja
// [protocol.Region]).
type TileSource interface {
	// Tiles devolve a grade: colunas, linhas e o lado do bloco, em pixels
	// do quadro.
	Tiles() (cols, rows, size int)
	// Dirty marca em dst (cols*rows) os blocos que mudaram desde a chamada
	// anterior.
	Dirty(dst []bool)
}

// StillReader lê a imagem atual sem compressão, no tamanho do vídeo (já
// reduzida, se for o caso), para os retalhos sem perda.
type StillReader interface {
	ReadStill(r image.Rectangle) (*image.RGBA, error)
}

// EncodeTimer informa quanto levou a captura + codificação do último quadro.
type EncodeTimer interface {
	EncodeTime() time.Duration
}

// SpeedTuner troca o equilíbrio do encoder: 0 é a melhor qualidade, 2 a
// codificação mais rápida. Devolve false se o encoder recusou.
type SpeedTuner interface {
	SetSpeed(level int) bool
}

// NativeSizer informa o tamanho real da tela capturada (antes da redução).
type NativeSizer interface {
	Native() (w, h int)
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
	MaxW      int // largura máxima do quadro; 0 = resolução nativa (ou a da janela do técnico)
	FPS       int
	MinKbps   int
	StartKbps int // alvo no começo da sessão, antes de medir a rede
	MaxKbps   int
}

// Presets disponíveis, pelo nome usado no protocolo. Os pisos são baixos de
// propósito: num upload fraco, o bitrate tem de poder descer até caber, em
// vez de ficar preso acima da banda (o que só derrubava o FPS e enchia a
// fila). O começo é moderado e o controle sobe quando a banda aguenta.
var Presets = map[string]Preset{
	"turbo":     {MaxW: 1280, FPS: 60, MinKbps: 1000, StartKbps: 4000, MaxKbps: 10000}, // latência mínima
	"balanced":  {MaxW: 1920, FPS: 60, MinKbps: 2000, StartKbps: 8000, MaxKbps: 20000}, // 1080p
	"quality":   {FPS: 60, MinKbps: 3000, StartKbps: 12000, MaxKbps: 40000},            // nativa, padrão
	"quality60": {FPS: 60, MinKbps: 5000, StartKbps: 25000, MaxKbps: 80000},            // máximo, LAN gigabit
}

// DefaultPreset é o preset usado até o técnico escolher outro.
const DefaultPreset = "quality"

// CongestedThreshold é quantas esperas por ACK (de ~3 ms cada) num segundo
// contam como congestionamento de verdade: ~30 ms parados. Abaixo disso é um
// soluço isolado da rede (comum através de túnel), que não justifica derrubar
// a qualidade.
const CongestedThreshold = 10
