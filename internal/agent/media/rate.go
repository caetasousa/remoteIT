package media

import "time"

// Limites de fila usados pelo controle de bitrate. A fila é o RTT médio do
// último segundo menos o menor RTT recente: o tempo que os quadros passam
// esperando em algum buffer (do roteador, do TCP, do túnel) além do caminho.
const (
	// QueueHigh: acima disso, a banda não está dando conta e o bitrate cai.
	QueueHigh = 80 * time.Millisecond
	// QueueLow: acima disso a fila está começando; o bitrate fica onde está.
	QueueLow = 30 * time.Millisecond
)

// Second resume o último segundo de transmissão para o [RateControl].
type Second struct {
	SentKbps int           // o que de fato saiu
	Frames   int           // quadros de vídeo enviados
	Waits    int           // esperas do controle de fluxo (veja CongestedThreshold)
	Queue    time.Duration // RTT médio menos o RTT mínimo
}

// RateControl escolhe o alvo de bitrate do encoder a cada segundo.
//
// Corta quando há fila de verdade (esperas por ACK ou atraso acima do RTT
// mínimo), segura quando a fila começa e só sobe quando o alvo atual foi de
// fato usado sem fila. Essa última regra é a que importa numa tela de
// escritório: com a tela quase parada, o encoder manda bem menos que o alvo,
// e subir o alvo nesse tempo (como antes) fazia a primeira rolagem sair com
// o bitrate máximo e entupir o link.
type RateControl struct {
	p    Preset
	kbps int
}

// Reset volta ao bitrate inicial do preset (início da sessão ou troca de
// preset).
func (r *RateControl) Reset(p Preset) { r.p, r.kbps = p, p.StartKbps }

// Kbps é o alvo atual.
func (r *RateControl) Kbps() int { return r.kbps }

// Next calcula o alvo do próximo segundo.
func (r *RateControl) Next(s Second) int {
	switch {
	case s.Waits >= CongestedThreshold || s.Queue > QueueHigh:
		r.kbps = max(r.p.MinKbps, r.kbps*3/4)
	case s.Waits > 0 || s.Queue > QueueLow:
		// soluço ou fila começando: segura
	case s.Frames > 0 && s.SentKbps*10 >= r.kbps*6:
		r.kbps = min(r.p.MaxKbps, r.kbps*6/5+50)
	}
	return r.kbps
}
