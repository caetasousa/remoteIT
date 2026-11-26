package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"remoteit/internal/protocol"
	"remoteit/internal/scale"
)

// Stream é uma transmissão de vídeo para um técnico. Crie com [NewStream],
// rode [Stream.Run] em uma goroutine e repasse a ela as mensagens de controle
// do técnico (OnHello, SetPreset, RequestKeyframe, OnAck).
type Stream struct {
	enc    Encoders
	send   Sender
	logger *slog.Logger
	flow   FlowControl

	codecs   atomic.Value // []string: o que o técnico decodifica, em ordem de preferência
	hello    chan struct{}
	helloMu  sync.Once
	wantKey  atomic.Bool
	preset   atomic.Value  // string
	viewport atomic.Uint64 // largura<<32 | altura da área do técnico (0 = nativa)
	reconf   chan struct{}

	// Estado que sobrevive à troca da thread de captura (veja Run). Só uma
	// goroutine de captura roda por vez; o canal entre elas ordena o acesso.
	failed     map[string]bool // codecs que falharam nesta máquina: não tenta de novo
	cur        string          // codec da fonte aberta
	blocked    bool
	lastFollow string
	rate       RateControl // alvo de bitrate: sobrevive à reabertura da fonte
	ratePreset string      // preset com que rate foi iniciado
}

// NewStream cria uma transmissão que envia por send usando os encoders enc.
func NewStream(enc Encoders, send Sender, logger *slog.Logger) *Stream {
	s := &Stream{enc: enc, send: send, logger: logger, hello: make(chan struct{}), reconf: make(chan struct{}, 1),
		failed: map[string]bool{}}
	s.preset.Store(DefaultPreset)
	s.codecs.Store([]string{protocol.CodecNameJPEG})
	return s
}

// OnHello registra os codecs que o técnico decodifica, em ordem de
// preferência (veja protocol.Message.Accepts). Um hello diferente do
// anterior reabre a fonte: é assim que o técnico troca de codec, ou desiste
// de um que o decodificador dele não aguentou.
func (s *Stream) OnHello(codecs []string) {
	old := s.codecs.Swap(slices.Clone(codecs)).([]string)
	s.helloMu.Do(func() { close(s.hello) })
	if !slices.Equal(old, codecs) {
		s.kick()
	}
}

// SetPreset troca o preset de qualidade; nomes desconhecidos são ignorados.
func (s *Stream) SetPreset(name string) {
	if _, ok := Presets[name]; ok && s.preset.Swap(name) != name {
		s.kick()
	}
}

// SetViewport informa o tamanho, em pixels físicos, da área onde o técnico
// mostra a tela (0 x 0 = resolução nativa, como no "Tamanho real"). O vídeo
// sai reduzido para caber nela; a fonte só é reaberta se o tamanho de saída
// mudar de fato.
func (s *Stream) SetViewport(w, h int) {
	v := uint64(uint32(max(w, 0)))<<32 | uint64(uint32(max(h, 0)))
	if s.viewport.Swap(v) != v {
		s.kick()
	}
}

// bounds são os limites de saída: os do preset e os da área do técnico.
func (s *Stream) bounds(p Preset) (maxw, maxh int) {
	v := s.viewport.Load()
	vw, vh := int(v>>32), int(uint32(v))
	maxw = p.MaxW
	if vw > 0 && vh > 0 {
		if maxw == 0 || vw < maxw {
			maxw = vw
		}
		maxh = vh
	}
	return maxw, maxh
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
	select { // espera o técnico dizer o que decodifica
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
		par       Params   // com que a fonte aberta foi criada
		parCodecs []string // a lista do hello naquela abertura
		parPreset string
		interval  time.Duration
		next      time.Time
		refine    int
		followErr error
		lastW     int
		lastH     int
		waits     int
		frames    int
		bytes     int
		encSum    time.Duration
		encN      int
		speed     int // 0 = qualidade ... 2 = velocidade (veja SpeedTuner)
		slow      int // segundos seguidos com o encoder lento
		winStart  = time.Now()
		rttReset  = time.Now()
		lastShape string
		lastCur   protocol.CursorPos
		curAt     time.Time
		buf       []byte
		tiles     *tileState
		dirty     []bool
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
		name := s.preset.Load().(string)
		p = Presets[name]
		if name != s.ratePreset {
			s.rate.Reset(p)
			s.ratePreset = name
		}
		maxw, maxh := s.bounds(p)
		codecs := s.codecs.Load().([]string)
		for _, c := range codecs {
			want := Params{MaxW: maxw, MaxH: maxh, FPS: p.FPS, Kbps: s.rate.Kbps()}
			v, err := s.openCodec(c, p, want)
			if v == nil && err == nil {
				continue // não existe neste build ou não serve para este preset
			}
			if err == nil {
				src, s.cur = v, c
				par, parCodecs, parPreset = want, codecs, name
				return nil
			}
			if errors.Is(err, ErrReopen) || c == protocol.CodecNameJPEG {
				return err // JPEG é a última saída: nunca fica marcado como falho
			}
			s.logger.Info("codec indisponível, tentando o próximo", "codec", c, "err", err)
			s.failed[c] = true
		}
		return errors.New("nenhum codec disponível")
	}

	// sameOutput informa se a mudança pedida (só a área do técnico) deixa o
	// quadro do mesmo tamanho: aí não vale reabrir a fonte e mandar keyframe.
	sameOutput := func() bool {
		name := s.preset.Load().(string)
		if src == nil || name != parPreset || !slices.Equal(s.codecs.Load().([]string), parCodecs) {
			return false
		}
		ns, ok := src.(NativeSizer)
		if !ok {
			return false
		}
		w, h := ns.Native()
		maxw, maxh := s.bounds(Presets[name])
		ow, oh := scale.Fit(w, h, maxw, maxh)
		pw, ph := scale.Fit(w, h, par.MaxW, par.MaxH)
		return ow == pw && oh == ph
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
			s.logger.Info("captura aberta", "encoder", src.Name(), "limite", fmt.Sprintf("%dx%d", par.MaxW, par.MaxH))
			interval = time.Second / time.Duration(p.FPS)
			s.flow.SetInterval(interval)
			refine, tiles, speed, slow = 0, nil, 0, 0
			still.noReader = false
			s.RequestKeyframe()
		}

		select {
		case <-s.reconf:
			if !sameOutput() {
				src.Close()
				src = nil
				continue
			}
			par.MaxW, par.MaxH = s.bounds(p) // mesmo tamanho de saída: só registra
		default:
		}

		if s.enc.CursorShape != nil {
			if cs := s.enc.CursorShape(); cs != lastShape {
				lastShape = cs
				s.send.SendJSON(protocol.CursorShape{T: protocol.TypeCursorShape, Shape: cs})
			}
		}
		// a posição do cursor vai à parte, sem codificar quadro: o DXGI acorda
		// a captura quando só o mouse mexe, e codificar a mesma imagem só
		// gastava banda, uma vaga do controle de fluxo e o refresh sem perda
		if cr, ok := src.(CursorReporter); ok && time.Since(curAt) >= 15*time.Millisecond {
			x, y, vis := cr.CursorPos()
			if c := (protocol.CursorPos{T: protocol.TypeCursorPos, X: x, Y: y, Visible: vis}); c != lastCur {
				lastCur, curAt = c, time.Now()
				s.send.SendJSON(c)
			}
		}

		if d := time.Until(next); d > 0 && !sleepCtx(ctx, d) {
			return false
		}

		if s.flow.Congested(time.Now()) {
			waits++
			if !sleepCtx(ctx, 3*time.Millisecond) {
				return false
			}
		} else {
			if shot, ok := still.take(); ok && tiles != nil {
				sent, stale := s.sendStill(shot, tiles, &buf, &bytes)
				if shot.full {
					still.fullFailed = stale // tela inteira velha: tente retalhos da próxima vez
				}
				if sent {
					continue
				}
			}
			flags, repeat := 0, false
			timeout := interval // espera no máximo 1 quadro; evita lag acumulado
			if s.wantKey.Swap(false) {
				flags, timeout = FlagKey|FlagRepeat, 0
			}
			f, err := src.Next(timeout, flags)
			// refina o último quadro (o encoder melhora a imagem parada), mas
			// só se o técnico ainda mostra vídeo em algum lugar
			if err == nil && f == nil && refine > 0 && (tiles == nil || tiles.anyLossy()) {
				refine--
				repeat = true
				f, err = src.Next(0, FlagRepeat)
			}
			if err != nil {
				src.Close()
				src = nil
				if !errors.Is(err, ErrReopen) && s.cur != protocol.CodecNameJPEG {
					s.logger.Info("erro no encoder, trocando de codec", "codec", s.cur, "err", err)
					s.failed[s.cur] = true
				}
				continue
			}
			if f != nil {
				now := time.Now()
				ts, tiled := src.(TileSource)
				full := tiles == nil || tiles.w != f.W || tiles.h != f.H || f.Keyframe
				if tiles == nil || tiles.w != f.W || tiles.h != f.H {
					cols, rows, size := 1, 1, max(f.W, f.H)
					if tiled {
						cols, rows, size = ts.Tiles()
					}
					tiles, dirty = newTileState(f.W, f.H, cols, rows, size), make([]bool, cols*rows)
				}
				if tiled {
					clear(dirty)
					ts.Dirty(dirty) // esvazia sempre, para não sobrar mudança velha
				}
				switch {
				case full || !tiled:
					tiles.markAll(now)
				case !repeat:
					tiles.mark(dirty, now)
				}
				if !repeat {
					refine = 2
				}
				seq := s.flow.Sent(now)
				if tiled && !full && !tiles.allLossy() {
					s.send.SendJSON(protocol.Region{T: protocol.TypeRegion, S: seq, R: flat(tiles.lossyRects())})
				}
				buf = protocol.AppendFrame(buf[:0], protocol.FrameHeader{
					Codec: f.Codec, Keyframe: f.Keyframe, W: uint16(f.W), H: uint16(f.H), Seq: seq,
				}, f.Data)
				if s.send.SendBinary(buf) != nil {
					return false
				}
				if et, ok := src.(EncodeTimer); ok {
					encSum += et.EncodeTime()
					encN++
				}
				frames++
				bytes += len(buf)
				lastW, lastH = f.W, f.H
				if next.Before(now.Add(-interval)) {
					next = now
				}
				next = next.Add(interval)
			}
			// blocos que pararam de mudar ganham o retalho sem perda, mesmo
			// que outra parte da tela continue mexendo (um cursor de texto
			// piscando não pode impedir o resto de ficar nítido)
			if tiles != nil && !still.pending {
				if idx := tiles.settled(time.Now(), StillDelay); len(idx) > 0 {
					reader, _ := src.(StillReader)
					if still.noReader {
						reader = nil
					}
					still.shoot(tiles, idx, reader, par.MaxW == 0 && par.MaxH == 0)
				}
			}
		}

		if time.Since(winStart) >= time.Second {
			rtt, minRTT := s.flow.TakeRTT(), s.flow.MinRTT()
			var queue time.Duration
			if rtt > 0 && minRTT > 0 {
				queue = max(0, rtt-minRTT)
			}
			if nk := s.rate.Next(Second{SentKbps: bytes * 8 / 1000, Frames: frames, Waits: waits, Queue: queue}); nk != par.Kbps {
				par.Kbps = nk
				src.SetBitrate(nk)
			}
			var encUs int
			if encN > 0 {
				avg := encSum / time.Duration(encN)
				encUs = int(avg.Microseconds())
				// encoder que não dá conta do quadro no tempo dele: troca
				// qualidade por velocidade (uma vez por nível, sem voltar)
				if st, ok := src.(SpeedTuner); ok && speed < 2 {
					if avg > interval*6/10 {
						slow++
					} else {
						slow = 0
					}
					if slow >= 2 {
						speed, slow = speed+1, 0
						s.logger.Info("encoder lento: priorizando velocidade", "tempo", avg, "nivel", speed, "aceito", st.SetSpeed(speed))
					}
				}
			}
			s.send.SendJSON(protocol.Stats{T: protocol.TypeStats, Encoder: src.Name(), FPS: frames,
				Kbps: bytes * 8 / 1000, Target: par.Kbps, RTTms: s.flow.RTT().Milliseconds(), W: lastW, H: lastH,
				RTTMin: minRTT.Milliseconds(), EncodeUs: encUs})
			frames, bytes, waits, encSum, encN, winStart = 0, 0, 0, 0, 0, time.Now()
			if time.Since(rttReset) > 10*time.Second {
				s.flow.ResetMinRTT()
				rttReset = time.Now()
			}
		}
	}
	return false
}

// sendStill manda o refresh sem perda pronto, se ele ainda vale: o quadro
// continua do mesmo tamanho e os blocos que ele cobre não mudaram desde a
// captura (stale = algum mudou). O teto de 2 s de banda do alvo atual evita
// que um papel de parede fotográfico em 4K entupa a conexão: uma imagem
// maior que isso é abandonada até a tela mudar ali.
func (s *Stream) sendStill(shot stillShot, t *tileState, buf *[]byte, bytes *int) (sent, stale bool) {
	if shot.w != t.w || shot.h != t.h {
		return false, true
	}
	budget := s.rate.Kbps() * 250
	if shot.full {
		pt := shot.parts[0]
		if !t.same(pt.tiles, pt.gens) {
			return false, true
		}
		if len(pt.png) > budget {
			t.clean(pt.tiles)
			return false, false
		}
		seq := s.flow.Sent(time.Now())
		*buf = protocol.AppendFrame((*buf)[:0], protocol.FrameHeader{
			Codec: protocol.CodecPNG, W: uint16(t.w), H: uint16(t.h), Seq: seq,
		}, pt.png)
		if s.send.SendBinary(*buf) != nil {
			return false, false
		}
		*bytes += len(*buf)
		t.clean(pt.tiles)
		return true, false
	}
	var rects []protocol.PNGRect
	var done [][]int
	size := 0
	for _, pt := range shot.parts {
		switch {
		case !t.same(pt.tiles, pt.gens):
			stale = true // mudou durante a compressão: o vídeo já mostra o novo
		case len(pt.png) > budget:
			t.clean(pt.tiles)
		case size+len(pt.png) <= budget:
			size += len(pt.png)
			rects = append(rects, protocol.PNGRect{X: pt.x, Y: pt.y, PNG: pt.png})
			done = append(done, pt.tiles)
		}
	}
	if len(rects) == 0 {
		return false, stale
	}
	seq := s.flow.Sent(time.Now())
	*buf = protocol.AppendFrame((*buf)[:0], protocol.FrameHeader{
		Codec: protocol.CodecPNGRect, W: uint16(t.w), H: uint16(t.h), Seq: seq,
	}, protocol.AppendPNGRects(nil, rects))
	if s.send.SendBinary(*buf) != nil {
		return false, stale
	}
	*bytes += len(*buf)
	for _, tl := range done {
		t.clean(tl)
	}
	return true, stale
}

// openCodec abre a fonte de um codec. (nil, nil) quer dizer "pule este":
// o build não tem o encoder, ele já falhou antes ou não serve para o preset.
func (s *Stream) openCodec(name string, p Preset, par Params) (Source, error) {
	if s.failed[name] {
		return nil, nil
	}
	switch name {
	case protocol.CodecNameAVC444:
		// só nos presets sem redução própria: os reduzidos economizam banda,
		// e o 4:4:4 custa dois encoders e quase o dobro de bytes
		if s.enc.AVC444 != nil && p.MaxW == 0 {
			return s.enc.AVC444(par)
		}
	case protocol.CodecNameAV1:
		if s.enc.AV1 != nil {
			return s.enc.AV1(par)
		}
	case protocol.CodecNameH264:
		if s.enc.H264 != nil {
			return s.enc.H264(par)
		}
	case protocol.CodecNameJPEG:
		if s.enc.JPEG != nil {
			return s.enc.JPEG(par)
		}
	}
	return nil, nil
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
