// Package session controla uma conexão do agente com o relay: abre e fecha
// transmissões quando técnicos entram e saem, pede consentimento e decide
// quais mensagens chegam ao vídeo e ao input.
//
// Regras deste contexto:
//
//   - "start" abre uma transmissão nova e encerra a anterior.
//   - Nenhum quadro é enviado e nenhum input é aplicado antes do
//     consentimento do usuário remoto.
//   - "stop" encerra a transmissão; input posterior é descartado.
package session

import (
	"context"
	"log/slog"
	"sync/atomic"

	"remoteit/internal/agent/consent"
	"remoteit/internal/agent/input"
	"remoteit/internal/agent/media"
	"remoteit/internal/protocol"
)

// Stream é a parte de [media.Stream] que o controlador usa. Os testes
// injetam uma implementação falsa.
type Stream interface {
	Run(ctx context.Context)
	OnHello(h264 bool)
	SetPreset(name string)
	RequestKeyframe()
	OnAck(seq uint32)
}

// Config reúne as dependências do controlador.
type Config struct {
	Host    string // nome da máquina, mostrado na caixa de consentimento
	Consent consent.Asker
	Input   input.Injector
	// Lock bloqueia a estação de trabalho (veja internal/agent/lock). Nil
	// desliga o recurso: a mensagem é ignorada em vez de travar o processo.
	Lock func()
	// Clipboard põe um texto na área de transferência do PC. Nil desliga.
	Clipboard func(text string)
	NewStream func(media.Sender) Stream
	// OnActive, se definido, é avisado quando um técnico entra (true) e sai
	// (false). A atualização automática espera o fim do atendimento.
	OnActive func(active bool)
	Logger   *slog.Logger
}

// Controller trata as mensagens de uma conexão com o relay. Handle e Close
// devem ser chamados sempre da mesma goroutine (o loop de leitura).
type Controller struct {
	cfg  Config
	send media.Sender
	cur  *transmission
}

type transmission struct {
	cancel  context.CancelFunc
	stream  Stream
	granted atomic.Bool
}

// New cria o controlador que responde por send.
func New(cfg Config, send media.Sender) *Controller {
	return &Controller{cfg: cfg, send: send}
}

// Handle processa uma mensagem vinda do relay.
func (c *Controller) Handle(m protocol.Message) {
	if m.T == protocol.TypeStart {
		c.start()
		return
	}
	if m.T == protocol.TypeStop {
		c.stop()
		return
	}
	t := c.cur
	if t == nil {
		return
	}
	switch m.T {
	case protocol.TypeHello:
		t.stream.OnHello(m.H264)
	case protocol.TypeAck:
		t.stream.OnAck(m.S)
	case protocol.TypeKeyframe:
		t.stream.RequestKeyframe()
	case protocol.TypeConfig:
		t.stream.SetPreset(m.P)
	case protocol.TypeLock:
		// mesma exigência do input: só com consentimento já concedido
		if t.granted.Load() && c.cfg.Lock != nil {
			c.cfg.Lock()
		}
	case protocol.TypeClipboard:
		if t.granted.Load() && c.cfg.Clipboard != nil && len(m.Text) <= protocol.MaxClipboard {
			c.cfg.Clipboard(m.Text)
		}
	default:
		if m.IsInput() && t.granted.Load() {
			c.cfg.Input.Handle(m)
		}
	}
}

// Close encerra a transmissão em andamento, se houver.
func (c *Controller) Close() { c.stop() }

func (c *Controller) start() {
	c.stop()
	if c.cfg.OnActive != nil {
		c.cfg.OnActive(true)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &transmission{cancel: cancel, stream: c.cfg.NewStream(c.send)}
	c.cur = t
	go func() {
		c.send.SendJSON(protocol.Message{T: protocol.TypeWaiting})
		if !c.cfg.Consent(c.cfg.Host) {
			c.cfg.Logger.Info("acesso recusado pelo usuário")
			c.send.SendJSON(protocol.Message{T: protocol.TypeDenied})
			return
		}
		if ctx.Err() != nil { // o técnico saiu enquanto a caixa estava aberta
			return
		}
		t.granted.Store(true)
		c.send.SendJSON(protocol.Message{T: protocol.TypeGranted})
		t.stream.Run(ctx)
	}()
}

func (c *Controller) stop() {
	if c.cur != nil {
		c.cur.cancel()
		c.cur = nil
		if c.cfg.OnActive != nil {
			c.cfg.OnActive(false)
		}
	}
}
