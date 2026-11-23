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

// Mensagens enviadas pelo técnico (navegador) ao agente.
const (
	TypeHello     = "hello" // H264 informa se o navegador decodifica H.264
	TypeConfig    = "cfg"   // P escolhe um preset de qualidade
	TypeKeyframe  = "kf"    // pede keyframe depois de reiniciar o decoder
	TypeAck       = "ack"   // S confirma o quadro com esse número de sequência
	TypeMouseMove = "mm"    // X, Y em [0,1] relativos à tela remota
	TypeMouseDown = "md"    // X, Y e B (0 esquerdo, 1 meio, 2 direito)
	TypeMouseUp   = "mu"    // X, Y e B
	TypeWheel     = "wh"    // D em múltiplos de 120 (WHEEL_DELTA do Windows)
	TypeKeyDown   = "kd"    // Code e Key do KeyboardEvent do navegador
	TypeKeyUp     = "ku"    // Code e Key
	// TypeLock pede para bloquear a estação de trabalho (como Win+L). Não é
	// um evento de teclado: SendInput não alcança a troca de sessão para a
	// tela de bloqueio (Winlogon), então o agente chama LockWorkStation
	// diretamente. Veja internal/agent/lock.
	TypeLock = "lock"
)

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
	S    uint32  `json:"s,omitempty"`
	P    string  `json:"p,omitempty"`
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
