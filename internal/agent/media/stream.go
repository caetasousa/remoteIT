package media

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"remoteit/internal/protocol"
)

// Stream é uma transmissão de vídeo para um técnico. Crie com [NewStream],
// rode [Stream.Run] em uma goroutine e repasse a ela as mensagens de controle
// do técnico (OnHello, SetPreset, RequestKeyframe, OnAck).
type Stream struct {
	enc    Encoders
	send   Sender
	logger *slog.Logger
	flow   FlowControl

	h264    atomic.Bool
	hello   chan struct{}
	helloMu sync.Once
	wantKey atomic.Bool
	preset  atomic.Value // string
	reconf  chan struct{}

	// Estado que sobrevive à troca da thread de captura (veja Run). Só uma
	// goroutine de captura roda por vez; o canal entre elas ordena o acesso.
	noH264     bool
	blocked    bool
	lastFollow string
}

// NewStream cria uma transmissão que envia por send usando os encoders enc.
func NewStream(enc Encoders, send Sender, logger *slog.Logger) *Stream {
	s := &Stream{enc: enc, send: send, logger: logger, hello: make(chan struct{}), reconf: make(chan struct{}, 1)}
	s.preset.Store(DefaultPreset)
	return s
}

// OnHello registra se o navegador decodifica H.264. Mudar de ideia reabre a
// fonte com o outro codec.
func (s *Stream) OnHello(h264 bool) {
	old := s.h264.Swap(h264)
	s.helloMu.Do(func() { close(s.hello) })
	if old != h264 {
		s.kick()
	}
}

// SetPreset troca o preset de qualidade; nomes desconhecidos são ignorados.
func (s *Stream) SetPreset(name string) {
	if _, ok := Presets[name]; ok && s.preset.Swap(name) != name {
		s.kick()
	}
}

// RequestKeyframe pede um keyframe e descarta os quadros em trânsito.
func (s *Stream) RequestKeyframe() {
	s.flow.DropInflight()
	s.wantKey.Store(true)
}

// OnAck repassa a confirmação de um quadro ao controle de fluxo.
func (s *Stream) OnAck(seq uint32) { s.flow.OnAck(seq, time.Now()) }

func (s *Stream) kick() {
	select {
	case s.reconf <- struct{}{}:
	default:
	}
}

func (s *Stream) status(t string) { s.send.SendJSON(protocol.Message{T: t}) }

// Run transmite até ctx ser cancelado ou o envio falhar.
//
// A captura roda numa thread do SO só dela, porque COM/D3D/Media Foundation
// ficam presos à thread que os criou, e o desktop do Windows (Default ou a
// tela de login, Winlogon) também é por thread. Quando a tela troca de
// desktop, a thread precisa trocar junto, mas o Windows recusa a troca numa
// thread que já tem janelas, e o encoder H.264 cria janelas ocultas. Nesse
// caso a thread é descartada e a captura recomeça numa thread nova, limpa.
func (s *Stream) Run(ctx context.Context) {
	select { // espera o navegador dizer se decodifica H.264
	case <-s.hello:
	case <-time.After(1500 * time.Millisecond):
	case <-ctx.Done():
		return
	}
	select { // a mudança vinda do hello já vale para a primeira abertura
	case <-s.reconf:
	default:
	}
	for {
		again := make(chan bool, 1)
		go func() {
			// sem UnlockOSThread de propósito: assim o Go encerra a thread
			// junto com a goroutine, em vez de devolver ao pool uma thread
			// presa a um desktop
			runtime.LockOSThread()
			again <- s.capture(ctx)
		}()
		if !<-again || ctx.Err() != nil {
			return
		}
		s.logger.Info("recomeçando a captura numa thread nova", "desktop", s.lastFollow)
	}
}

// capture é o laço de captura de uma thread. Devolve true para pedir uma
// thread nova (esta não consegue mais seguir o desktop ativo) e false para
// encerrar a transmissão.
func (s *Stream) capture(ctx context.Context) bool {
	var (
		src       Source
		p         Preset
		kbps      int
		interval  time.Duration
		next      time.Time
		refine    int
		followErr error
		lastW     int
		lastH     int
		congested int
		frames    int
		bytes     int
		winStart  = time.Now()
		rttReset  = time.Now()
		lastShape string
		buf       []byte
		still     = newRefresher(s.enc.Still, s.logger)
	)
	defer func() {
		if src != nil {
			src.Close()
		}
		still.close()
	}()

	open := func() error {
		if s.enc.FollowDesktop != nil {
			// o erro não impede a tentativa: a abertura a seguir revela se o
			// desktop errado era mesmo o problema (veja o laço abaixo)
			followErr = s.enc.FollowDesktop()
			if msg := errText(followErr); msg != s.lastFollow {
				s.lastFollow = msg
				if followErr != nil {
					s.logger.Info("não foi possível seguir o desktop ativo", "err", followErr)
				}
			}
		}
		p = Presets[s.preset.Load().(string)]
		kbps = (p.MinKbps + p.MaxKbps) / 2
		if s.h264.Load() && !s.noH264 && s.enc.H264 != nil {
			v, err := s.enc.H264(p.MaxW, p.FPS, kbps)
			if err == nil {
				src = v
				return nil
			}
			if errors.Is(err, ErrReopen) {
				return err
			}
			s.logger.Info("H.264 indisponível, usando JPEG", "err", err)
			s.noH264 = true
		}
		v, err := s.enc.JPEG(p.MaxW, kbps)
		if err != nil {
			return err
		}
		src = v
		return nil
	}

	for ctx.Err() == nil {
		if src == nil {
			if err := open(); err != nil {
				if !s.blocked {
					s.logger.Info("captura indisponível", "err", err)
					s.status(protocol.TypeSecure)
					s.blocked = true
				}
				if !sleepCtx(ctx, 500*time.Millisecond) {
					return false
				}
				if followErr != nil {
					return true // esta thread ficou presa ao desktop antigo
				}
				continue
			}
			if s.blocked {
				s.status(protocol.TypeGranted)
				s.blocked = false
			}
			s.logger.Info("captura aberta", "encoder", src.Name())
			interval = time.Second / time.Duration(p.FPS)
			refine = 0
			s.RequestKeyframe()
		}

		select {
		case <-s.reconf:
			src.Close()
			src = nil
			continue
		default:
		}

		if s.enc.CursorShape != nil {
			if cs := s.enc.CursorShape(); cs != lastShape {
				lastShape = cs
				s.send.SendJSON(protocol.CursorShape{T: protocol.TypeCursorShape, Shape: cs})
			}
		}

		if d := time.Until(next); d > 0 && !sleepCtx(ctx, d) {
			return false
		}

		if s.flow.Congested(time.Now()) {
			congested++
			if !sleepCtx(ctx, 3*time.Millisecond) {
				return false
			}
		} else {
			// refresh de nitidez pronto: só vale se a tela não mudou desde a
			// captura e se tem o tamanho do vídeo (presets reduzidos mandam
			// menos pixels que a tela; o refresh nativo trocaria o tamanho do
			// canvas). O teto de 2 s de banda do alvo atual evita que um
			// papel de parede fotográfico em 4K entupa a conexão.
			if shot, ok := still.take(); ok && shot.w == lastW && shot.h == lastH && len(shot.data) <= kbps*250 {
				seq := s.flow.Sent(time.Now())
				buf = protocol.AppendFrame(buf[:0], protocol.FrameHeader{
					Codec: protocol.CodecPNG, W: uint16(shot.w), H: uint16(shot.h), Seq: seq,
				}, shot.data)
				if s.send.SendBinary(buf) != nil {
					return false
				}
				bytes += len(buf)
				continue
			}
			flags, repeat := 0, false
			timeout := interval // espera no máximo 1 quadro; evita lag acumulado
			if s.wantKey.Swap(false) {
				flags, timeout = FlagKey|FlagRepeat, 0
			}
			f, err := src.Next(timeout, flags)
			if err == nil && f == nil && refine > 0 {
				refine--
				repeat = true
				f, err = src.Next(0, FlagRepeat)
			}
			if err != nil {
				src.Close()
				src = nil
				if !errors.Is(err, ErrReopen) {
					s.logger.Info("erro na captura, caindo para JPEG", "err", err)
					s.noH264 = true
				}
				continue
			}
			if f != nil {
				now := time.Now()
				if !repeat {
					refine = 2
					still.changed(now)
				}
				seq := s.flow.Sent(now)
				buf = protocol.AppendFrame(buf[:0], protocol.FrameHeader{
					Codec: f.Codec, Keyframe: f.Keyframe, W: uint16(f.W), H: uint16(f.H), Seq: seq,
				}, f.Data)
				if s.send.SendBinary(buf) != nil {
					return false
				}
				if cr, ok := src.(CursorReporter); ok {
					x, y, vis := cr.CursorPos()
					s.send.SendJSON(protocol.CursorPos{T: protocol.TypeCursorPos, X: x, Y: y, Visible: vis})
				}
				frames++
				bytes += len(buf)
				lastW, lastH = f.W, f.H
				if next.Before(now.Add(-interval)) {
					next = now
				}
				next = next.Add(interval)
			} else if refine == 0 && p.MaxW == 0 && still.due(time.Now()) {
				still.shoot() // tela parada e já refinada pelo encoder
			}
		}

		if time.Since(winStart) >= time.Second {
			if nk := NextBitrate(kbps, p, congested, frames); nk != kbps {
				kbps = nk
				src.SetBitrate(kbps)
			}
			s.send.SendJSON(protocol.Stats{T: protocol.TypeStats, Encoder: src.Name(), FPS: frames,
				Kbps: bytes * 8 / 1000, Target: kbps, RTTms: s.flow.RTT().Milliseconds(), W: lastW, H: lastH})
			frames, bytes, congested, winStart = 0, 0, 0, time.Now()
			if time.Since(rttReset) > 10*time.Second {
				s.flow.ResetMinRTT()
				rttReset = time.Now()
			}
		}
	}
	return false
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
