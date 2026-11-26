// Package protocol define o contrato de mensagens compartilhado entre o
// agente, o relay e o console web.
//
// Existem dois tipos de mensagem WebSocket:
//
//   - Texto: um [Message] em JSON cujo campo T diz o tipo da mensagem (veja as
//     constantes Type*). Circula nos dois sentidos.
//   - Binária: um quadro de vídeo do agente para o técnico, formado por um
//     [FrameHeader] de [HeaderSize] bytes seguido do payload codificado.
//
// O relay repassa os dois tipos sem inspecioná-los. O espelho em TypeScript
// deste contrato fica em frontend/src/lib/protocol.ts e DEVE ser mantido igual.
package protocol

import "slices"

// Mensagens enviadas pelo técnico (navegador) ao agente.
const (
	// TypeHello diz o que o técnico decodifica: Codecs em ordem de
	// preferência (veja [Message.Accepts]). H264 é o formato antigo, mantido
	// para agentes de antes da negociação.
	TypeHello     = "hello"
	TypeConfig    = "cfg" // P escolhe um preset de qualidade
	TypeKeyframe  = "kf"  // pede keyframe depois de reiniciar o decoder
	TypeAck       = "ack" // S confirma o quadro com esse número de sequência
	TypeMouseMove = "mm"  // X, Y em [0,1] relativos à tela remota
	TypeMouseDown = "md"  // X, Y e B (0 esquerdo, 1 meio, 2 direito)
	TypeMouseUp   = "mu"  // X, Y e B
	TypeWheel     = "wh"  // D em múltiplos de 120 (WHEEL_DELTA do Windows)
	TypeKeyDown   = "kd"  // Code e Key do KeyboardEvent do navegador
	TypeKeyUp     = "ku"  // Code e Key
	// TypeLock pede para bloquear a estação de trabalho (como Win+L). Não é
	// um evento de teclado: SendInput não alcança a troca de sessão para a
	// tela de bloqueio (Winlogon), então o agente chama LockWorkStation
	// diretamente. Veja internal/agent/lock.
	TypeLock = "lock"
	// TypeClipboard põe Text na área de transferência do PC remoto (o
	// técnico cola com Ctrl+V lá). Como o input, só com consentimento.
	TypeClipboard = "clip"
)

// MaxClipboard é o maior texto aceito em TypeClipboard (em bytes).
const MaxClipboard = 1 << 20

// Mensagens enviadas pelo relay ao agente.
const (
	TypeStart = "start" // um técnico conectou: pedir consentimento e transmitir
	TypeStop  = "stop"  // o técnico saiu: parar a transmissão
)

// Mensagens enviadas pelo agente ao técnico.
const (
	TypeWaiting     = "waiting" // caixa de consentimento aberta na máquina remota
	TypeGranted     = "granted" // consentimento dado, quadros a seguir
	TypeDenied      = "denied"  // o usuário remoto recusou
	TypeSecure      = "secure"  // captura bloqueada (UAC, sessão bloqueada)
	TypeStats       = "stats"   // uma vez por segundo, veja [Stats]
	TypeCursorShape = "cs"      // veja [CursorShape]
	TypeCursorPos   = "cur"     // veja [CursorPos]
	TypeRegion      = "rg"      // veja [Region]
)

// Message é a mensagem de controle genérica em JSON. Só os campos relevantes
// para T são preenchidos; os demais ficam no valor zero e são omitidos.
type Message struct {
	T    string  `json:"t"`
	X    float64 `json:"x,omitempty"`
	Y    float64 `json:"y,omitempty"`
	B    int     `json:"b,omitempty"`
	D    int     `json:"d,omitempty"`
	Code string  `json:"code,omitempty"`
	Key  string  `json:"key,omitempty"`
	H264 bool    `json:"h264,omitempty"`
	// Codecs é a lista do hello, em ordem de preferência: CodecName*.
	Codecs []string `json:"codecs,omitempty"`
	S      uint32   `json:"s,omitempty"`
	P      string   `json:"p,omitempty"`
	Text   string   `json:"text,omitempty"`
	// VW e VH vão no cfg: o tamanho, em pixels físicos, da área onde o
	// técnico mostra a tela ajustada à janela. O agente reduz o vídeo para
	// caber nela (veja media.Params). 0 (ou ausente, em técnicos antigos) =
	// resolução nativa, como no modo "Tamanho real".
	VW int `json:"vw,omitempty"`
	VH int `json:"vh,omitempty"`
}

// IsInput informa se a mensagem é um evento de mouse ou teclado. O agente só
// injeta esses eventos depois que o usuário remoto deu consentimento.
func (m Message) IsInput() bool {
	switch m.T {
	case TypeMouseMove, TypeMouseDown, TypeMouseUp, TypeWheel, TypeKeyDown, TypeKeyUp:
		return true
	}
	return false
}

// Accepts devolve os codecs que um hello aceita, em ordem de preferência. Um
// hello sem Codecs vem de um técnico de antes da negociação: o campo H264
// decide entre H.264 e JPEG. JPEG sempre entra no fim, porque todo cliente o
// decodifica e é a última saída do agente.
func (m Message) Accepts() []string {
	var out []string
	switch {
	case len(m.Codecs) > 0:
		for _, c := range m.Codecs {
			if c != CodecNameJPEG && !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	case m.H264:
		out = append(out, CodecNameH264)
	}
	return append(out, CodecNameJPEG)
}

// Stats descreve o estado do pipeline de vídeo, enviado uma vez por segundo.
type Stats struct {
	T       string `json:"t"`      // sempre TypeStats
	Encoder string `json:"enc"`    // nome legível do encoder em uso
	FPS     int    `json:"fps"`    // quadros enviados no último segundo
	Kbps    int    `json:"kbps"`   // bitrate medido
	Target  int    `json:"target"` // alvo do bitrate adaptativo
	RTTms   int64  `json:"rtt"`    // ida e volta do último quadro, em ms
	W       int    `json:"w"`
	H       int    `json:"h"`
	// RTTMin é o menor RTT recente (ms): a rede sem fila. RTTms - RTTMin é
	// quanto os quadros estão esperando em alguma fila no caminho.
	RTTMin int64 `json:"rttmin,omitempty"`
	// EncodeUs é o tempo médio de captura + codificação de um quadro no
	// último segundo, em microssegundos (0 = a fonte não mede).
	EncodeUs int `json:"encus,omitempty"`
}

// Region diz em que partes do próximo quadro de vídeo (o de número S) o
// técnico deve usar os pixels do vídeo; no resto, mantém o que já mostra,
// que veio sem perda dos retalhos PNG (CodecPNGRect). É mandada logo antes do
// quadro. R tem x, y, largura e altura de cada retângulo, em pixels do
// quadro; R vazio = nada muda na tela (o quadro só refina o decodificador).
// Sem Region, o quadro inteiro vale, como em técnicos e agentes antigos.
type Region struct {
	T string `json:"t"` // sempre TypeRegion
	S uint32 `json:"s"`
	R []int  `json:"r"`
}

// CursorShape informa qual cursor CSS imita o cursor remoto.
type CursorShape struct {
	T     string `json:"t"` // sempre TypeCursorShape
	Shape string `json:"c"` // nome CSS, por exemplo "text"
}

// CursorPos é a posição do cursor remoto em pixels da tela. O técnico desenha
// o cursor como overlay em vez de embuti-lo no vídeo, o que evita rastro.
type CursorPos struct {
	T       string `json:"t"` // sempre TypeCursorPos
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Visible bool   `json:"v"`
}
