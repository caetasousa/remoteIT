package media

import (
	"bytes"
	"image/png"
	"log/slog"
	"time"
)

// StillDelay é quanto a tela precisa ficar parada para o pipeline mandar o
// refresh de nitidez. Curto o bastante para o técnico não esperar ao ler um
// texto, longo o bastante para não disparar entre duas teclas digitadas.
const StillDelay = 300 * time.Millisecond

// stillShot é uma captura sem perda já codificada em PNG.
type stillShot struct {
	gen  uint64 // geração da tela quando foi capturada (veja refresher.gen)
	w, h int
	data []byte
}

// refresher cuida do "refresh de nitidez": quando a tela para de mudar, manda
// uma única imagem PNG sem perda por cima do vídeo.
//
// Por que existe: o H.264 guarda a cor com metade da resolução (4:2:0) e,
// em modo de baixa latência, não refina uma tela parada até a perfeição. Em
// vídeo ninguém nota; em tela de computador aparece como texto colorido
// borrado e franjas no ClearType. Programas instalados resolvem com H.264
// 4:4:4, que o decodificador do navegador não aceita. A saída no navegador é
// esta: vídeo enquanto a tela mexe, imagem exata quando ela para.
//
// Não é seguro para uso concorrente: só a goroutine de captura o usa. A
// captura em si roda nessa thread (precisa estar no mesmo desktop do
// Windows que o vídeo); só a compressão PNG, que é cara, vai para outra
// goroutine, para não travar o vídeo se a tela voltar a mexer.
type refresher struct {
	open    func() (Still, error)
	logger  *slog.Logger
	cap     Still
	gen     uint64    // muda a cada quadro novo; descarta refresh velho
	since   time.Time // quando a tela mudou pela última vez
	done    bool      // o refresh desta imagem já foi feito (ou tentado)
	pending bool      // há uma compressão em andamento
	failed  bool      // a falha já foi registrada no log
	ready   chan stillShot
}

func newRefresher(open func() (Still, error), logger *slog.Logger) *refresher {
	return &refresher{open: open, logger: logger, ready: make(chan stillShot, 1)}
}

// changed registra que um quadro novo (não repetido) foi enviado.
func (r *refresher) changed(now time.Time) {
	r.gen++
	r.since = now
	r.done = false
}

// due diz se já é hora de capturar o refresh.
func (r *refresher) due(now time.Time) bool {
	return r.open != nil && !r.done && !r.pending && !r.since.IsZero() && now.Sub(r.since) >= StillDelay
}

// shoot captura a tela agora e a comprime em segundo plano; o resultado sai
// por [refresher.take].
func (r *refresher) shoot() {
	r.done = true
	if r.cap == nil {
		c, err := r.open()
		if err != nil {
			r.fail(err)
			return
		}
		r.cap = c
	}
	img, err := r.cap.Capture()
	if err != nil {
		r.fail(err)
		return
	}
	r.pending = true
	gen := r.gen
	go func() {
		var b bytes.Buffer
		// BestSpeed: telas de computador têm grandes áreas lisas, que
		// comprimem bem mesmo no nível mais rápido; os níveis maiores custam
		// várias vezes mais CPU para ganhar pouco
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		shot := stillShot{gen: gen, w: img.Rect.Dx(), h: img.Rect.Dy()}
		if enc.Encode(&b, img) == nil {
			shot.data = b.Bytes()
		}
		r.ready <- shot // nunca bloqueia: só há uma compressão por vez (pending)
	}()
}

// take devolve o refresh pronto, se houver e se a tela não mudou desde a
// captura (senão ele mostraria uma imagem velha por cima da nova).
func (r *refresher) take() (stillShot, bool) {
	if !r.pending {
		return stillShot{}, false
	}
	select {
	case shot := <-r.ready:
		r.pending = false
		return shot, shot.data != nil && shot.gen == r.gen
	default:
		return stillShot{}, false
	}
}

func (r *refresher) fail(err error) {
	if !r.failed {
		r.failed = true
		r.logger.Info("refresh de nitidez indisponível", "err", err)
	}
}

func (r *refresher) close() {
	if r.cap != nil {
		r.cap.Close()
		r.cap = nil
	}
}
