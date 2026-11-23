//go:build windows

package input

import (
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"remoteit/internal/protocol"
)

var pSendInput = syscall.NewLazyDLL("user32.dll").NewProc("SendInput")

// Layout idêntico ao INPUT do Windows x64 (40 bytes): DWORD type + 4 bytes de
// alinhamento + union de 32 bytes (alinhada a 8 por causa do ULONG_PTR).
type mouseInput struct {
	typ               uint32
	_                 uint32
	dx, dy            int32
	data, flags, time uint32
	extra             uintptr
}

type keyInput struct {
	typ       uint32
	_         uint32
	vk, scan  uint16
	flags, tm uint32
	extra     uintptr
	_         [8]byte
}

// falha de compilação se o tamanho não for 40 bytes
var (
	_ [40 - unsafe.Sizeof(mouseInput{})]byte
	_ [unsafe.Sizeof(mouseInput{}) - 40]byte
	_ [40 - unsafe.Sizeof(keyInput{})]byte
	_ [unsafe.Sizeof(keyInput{}) - 40]byte
)

// send chama SendInput e, se o Windows recusar (devolve 0 eventos
// injetados), reanexa a thread ao desktop ativo e tenta uma vez mais. A recusa
// típica é a thread ainda estar no desktop Default enquanto a tela de
// bloqueio/login (Winlogon) recebe a entrada.
func (s *sendInput) send(p unsafe.Pointer, size uintptr) {
	if r, _, _ := pSendInput.Call(1, uintptr(p), size); r != 0 || s.follow == nil {
		return
	}
	if s.follow() == nil {
		pSendInput.Call(1, uintptr(p), size)
	}
}

func (s *sendInput) sendMouse(dx, dy int32, data, flags uint32) {
	in := mouseInput{typ: 0, dx: dx, dy: dy, data: data, flags: flags}
	s.send(unsafe.Pointer(&in), unsafe.Sizeof(in))
}

func (s *sendInput) sendKey(vk, scan uint16, flags uint32) {
	in := keyInput{typ: 1, vk: vk, scan: scan, flags: flags}
	s.send(unsafe.Pointer(&in), unsafe.Sizeof(in))
}

// mouse: botão do navegador -> flags MOUSEEVENTF_*DOWN / *UP
var mouseButtons = map[int][2]uint32{0: {0x2, 0x4}, 1: {0x20, 0x40}, 2: {0x8, 0x10}}

// sendInput injeta numa única thread do SO, dedicada (veja loop). O desktop
// de uma thread no Windows é por thread: SendInput só alcança o desktop em
// que a thread que o chama está. A goroutine de leitura da conexão pula de
// thread em thread, então não dá para injetar dali e acertar o desktop.
type sendInput struct {
	events chan protocol.Message
	follow func() error

	// só a goroutine de loop toca nestes campos
	ctrl, alt, meta bool
	unicodeDown     map[string]bool // teclas cujo keydown foi enviado como caractere
}

// New cria o injetor SendInput. followDesktop (opcional) reanexa a thread
// atual ao desktop que recebe entrada agora; sem ele, a entrada só funciona
// no desktop em que o processo nasceu.
func New(followDesktop func() error) Injector {
	s := &sendInput{
		events:      make(chan protocol.Message, 256),
		follow:      followDesktop,
		unicodeDown: map[string]bool{},
	}
	go s.loop()
	return s
}

// Handle enfileira o evento para a thread de injeção. A fila cheia (técnico
// mandando mais rápido do que o Windows aceita) descarta movimentos de mouse,
// que o próximo movimento corrige, mas nunca cliques nem teclas: perder um
// keyup deixaria a tecla presa.
func (s *sendInput) Handle(m protocol.Message) {
	if m.T == protocol.TypeMouseMove {
		select {
		case s.events <- m:
		default:
		}
		return
	}
	s.events <- m
}

func (s *sendInput) loop() {
	runtime.LockOSThread() // nunca destrava: a thread vive o processo inteiro
	var followed time.Time
	for m := range s.events {
		// além da nova tentativa em send, confere o desktop a cada 1 s: o
		// SendInput nem sempre recusa quando a thread está no desktop errado,
		// às vezes aceita e o evento some
		if s.follow != nil && time.Since(followed) > time.Second {
			s.follow()
			followed = time.Now()
		}
		s.apply(m)
	}
}

func (s *sendInput) apply(m protocol.Message) {
	switch m.T {
	case protocol.TypeMouseMove:
		s.sendMouse(int32(m.X*65535), int32(m.Y*65535), 0, 0x8000|0x1) // ABSOLUTE|MOVE, monitor principal
	case protocol.TypeMouseDown, protocol.TypeMouseUp:
		f, ok := mouseButtons[m.B]
		if !ok {
			return
		}
		if m.T == protocol.TypeMouseDown {
			s.sendMouse(int32(m.X*65535), int32(m.Y*65535), 0, 0x8000|0x1)
			s.sendMouse(0, 0, 0, f[0])
		} else {
			s.sendMouse(0, 0, 0, f[1])
		}
	case protocol.TypeWheel:
		s.sendMouse(0, 0, uint32(int32(-m.D)), 0x800) // WHEEL
	case protocol.TypeKeyDown, protocol.TypeKeyUp:
		s.key(m, m.T == protocol.TypeKeyDown)
	}
}

func (s *sendInput) key(m protocol.Message, down bool) {
	switch m.Code {
	case "ControlLeft", "ControlRight":
		s.ctrl = down
	case "AltLeft", "AltRight":
		s.alt = down
	case "MetaLeft", "MetaRight":
		s.meta = down
	}
	up := uint32(0)
	if !down {
		up = 0x2 // KEYEVENTF_KEYUP
	}
	// Caractere imprimível sem Ctrl/Alt/Win: envia como Unicode, independente
	// do layout. O keyup segue o mesmo caminho do keydown correspondente.
	r := []rune(m.Key)
	printable := len(r) == 1 && !s.ctrl && !s.alt && !s.meta && r[0] >= 0x20 && r[0] != 0x7f
	if printable && (down || s.unicodeDown[m.Code]) {
		if down {
			s.unicodeDown[m.Code] = true
		} else {
			delete(s.unicodeDown, m.Code)
		}
		for _, u := range utf16.Encode(r) {
			s.sendKey(0, u, 0x4|up) // KEYEVENTF_UNICODE
		}
		return
	}
	if vk, ext := vkFor(m.Code); vk != 0 {
		f := up
		if ext {
			f |= 0x1 // KEYEVENTF_EXTENDEDKEY
		}
		s.sendKey(vk, 0, f)
	}
}
