//go:build windows

package capture

import (
	"errors"
	"image"
	"syscall"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")

	pGetDC            = user32.NewProc("GetDC")
	pReleaseDC        = user32.NewProc("ReleaseDC")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	pGetCursorInfo    = user32.NewProc("GetCursorInfo")
	pLoadCursorW      = user32.NewProc("LoadCursorW")

	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pGetDIBits              = gdi32.NewProc("GetDIBits")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
)

type bmiHeader struct {
	Size                   uint32
	Width, Height          int32
	Planes, BitCount       uint16
	Compression, SizeImage uint32
	XPels, YPels           int32
	ClrUsed, ClrImportant  uint32
}

type gdiCap struct {
	w, h int
	mdc  uintptr
	bmp  uintptr
	old  uintptr
	buf  []byte
}

// New cria um capturador GDI. O DC e o bitmap são criados na primeira
// captura e recriados quando a resolução muda.
func New() (Capturer, error) { return &gdiCap{}, nil }

func (g *gdiCap) release() {
	if g.mdc != 0 {
		pSelectObject.Call(g.mdc, g.old)
		pDeleteObject.Call(g.bmp)
		pDeleteDC.Call(g.mdc)
		g.mdc, g.bmp = 0, 0
	}
}

func (g *gdiCap) Close() { g.release() }

func (g *gdiCap) Capture() (*image.RGBA, error) {
	w, _, _ := pGetSystemMetrics.Call(0)
	h, _, _ := pGetSystemMetrics.Call(1)
	if w == 0 || h == 0 {
		return nil, errors.New("sem tela")
	}
	sdc, _, _ := pGetDC.Call(0)
	if sdc == 0 {
		return nil, errors.New("GetDC falhou (sessão bloqueada?)")
	}
	defer pReleaseDC.Call(0, sdc)

	if g.mdc == 0 || g.w != int(w) || g.h != int(h) {
		g.release()
		g.w, g.h = int(w), int(h)
		g.mdc, _, _ = pCreateCompatibleDC.Call(sdc)
		g.bmp, _, _ = pCreateCompatibleBitmap.Call(sdc, w, h)
		g.old, _, _ = pSelectObject.Call(g.mdc, g.bmp)
		g.buf = make([]byte, g.w*g.h*4)
	}
	// SRCCOPY | CAPTUREBLT (inclui janelas em camadas/transparentes)
	if r, _, _ := pBitBlt.Call(g.mdc, 0, 0, w, h, sdc, 0, 0, 0x00CC0020|0x40000000); r == 0 {
		return nil, errors.New("BitBlt falhou")
	}
	hdr := bmiHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	if r, _, _ := pGetDIBits.Call(g.mdc, g.bmp, 0, h, uintptr(unsafe.Pointer(&g.buf[0])), uintptr(unsafe.Pointer(&hdr)), 0); r == 0 {
		return nil, errors.New("GetDIBits falhou")
	}
	img := image.NewRGBA(image.Rect(0, 0, g.w, g.h))
	for i := 0; i < len(g.buf); i += 4 { // BGRA -> RGBA
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = g.buf[i+2], g.buf[i+1], g.buf[i], 255
	}
	return img, nil
}

type cursorInfo struct {
	cbSize  uint32
	flags   uint32
	hCursor uintptr
	x, y    int32
}

// IDs dos cursores padrão do Windows -> nomes CSS equivalentes.
var cursorIDs = map[uintptr]string{
	32512: "default", 32513: "text", 32514: "wait", 32515: "crosshair", 32516: "default",
	32642: "nwse-resize", 32643: "nesw-resize", 32644: "ew-resize", 32645: "ns-resize",
	32646: "move", 32648: "not-allowed", 32649: "pointer", 32650: "progress", 32651: "help",
}

var cursorNames map[uintptr]string

// CursorShape devolve o formato atual do cursor do sistema como nome CSS.
// Cursores próprios de aplicativos viram "default". Não é seguro para uso
// concorrente: só o loop de stream deve chamá-la.
func CursorShape() string {
	if cursorNames == nil {
		cursorNames = make(map[uintptr]string, len(cursorIDs))
		for id, name := range cursorIDs {
			if h, _, _ := pLoadCursorW.Call(0, id); h != 0 {
				cursorNames[h] = name
			}
		}
	}
	ci := cursorInfo{cbSize: uint32(unsafe.Sizeof(cursorInfo{}))}
	if r, _, _ := pGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r == 0 || ci.flags&1 == 0 {
		return "default"
	}
	if n, ok := cursorNames[ci.hCursor]; ok {
		return n
	}
	return "default"
}
