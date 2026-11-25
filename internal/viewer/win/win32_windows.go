//go:build windows

package win

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassExW       = user32.NewProc("RegisterClassExW")
	pCreateWindowExW        = user32.NewProc("CreateWindowExW")
	pDefWindowProcW         = user32.NewProc("DefWindowProcW")
	pDestroyWindow          = user32.NewProc("DestroyWindow")
	pShowWindow             = user32.NewProc("ShowWindow")
	pUpdateWindow           = user32.NewProc("UpdateWindow")
	pGetMessageW            = user32.NewProc("GetMessageW")
	pTranslateMessage       = user32.NewProc("TranslateMessage")
	pDispatchMessageW       = user32.NewProc("DispatchMessageW")
	pIsDialogMessageW       = user32.NewProc("IsDialogMessageW")
	pPostMessageW           = user32.NewProc("PostMessageW")
	pSendMessageW           = user32.NewProc("SendMessageW")
	pPostQuitMessage        = user32.NewProc("PostQuitMessage")
	pSetWindowTextW         = user32.NewProc("SetWindowTextW")
	pGetWindowTextW         = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW   = user32.NewProc("GetWindowTextLengthW")
	pEnableWindow           = user32.NewProc("EnableWindow")
	pSetFocus               = user32.NewProc("SetFocus")
	pGetFocus               = user32.NewProc("GetFocus")
	pInvalidateRect         = user32.NewProc("InvalidateRect")
	pBeginPaint             = user32.NewProc("BeginPaint")
	pEndPaint               = user32.NewProc("EndPaint")
	pGetClientRect          = user32.NewProc("GetClientRect")
	pFillRect               = user32.NewProc("FillRect")
	pDrawTextW              = user32.NewProc("DrawTextW")
	pLoadCursorW            = user32.NewProc("LoadCursorW")
	pSetCursor              = user32.NewProc("SetCursor")
	pSetCapture             = user32.NewProc("SetCapture")
	pReleaseCapture         = user32.NewProc("ReleaseCapture")
	pCreateMenu             = user32.NewProc("CreateMenu")
	pCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	pAppendMenuW            = user32.NewProc("AppendMenuW")
	pSetMenu                = user32.NewProc("SetMenu")
	pCheckMenuItem          = user32.NewProc("CheckMenuItem")
	pCheckMenuRadioItem     = user32.NewProc("CheckMenuRadioItem")
	pDrawMenuBar            = user32.NewProc("DrawMenuBar")
	pSetWindowsHookExW      = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx    = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx         = user32.NewProc("CallNextHookEx")
	pGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	pGetKeyState            = user32.NewProc("GetKeyState")
	pToUnicodeEx            = user32.NewProc("ToUnicodeEx")
	pGetKeyboardLayout      = user32.NewProc("GetKeyboardLayout")
	pOpenClipboard          = user32.NewProc("OpenClipboard")
	pCloseClipboard         = user32.NewProc("CloseClipboard")
	pGetClipboardData       = user32.NewProc("GetClipboardData")
	pGetWindowLongPtrW      = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLongPtrW      = user32.NewProc("SetWindowLongPtrW")
	pSetWindowPos           = user32.NewProc("SetWindowPos")
	pGetWindowRect          = user32.NewProc("GetWindowRect")
	pMonitorFromWindow      = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW        = user32.NewProc("GetMonitorInfoW")
	pMessageBoxW            = user32.NewProc("MessageBoxW")
	pSetProcessDpiAwareness = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetDpiForWindow        = user32.NewProc("GetDpiForWindow")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")

	pStretchDIBits     = gdi32.NewProc("StretchDIBits")
	pSetStretchBltMode = gdi32.NewProc("SetStretchBltMode")
	pCreateSolidBrush  = gdi32.NewProc("CreateSolidBrush")
	pCreateFontW       = gdi32.NewProc("CreateFontW")
	pSelectObject      = gdi32.NewProc("SelectObject")
	pSetTextColor      = gdi32.NewProc("SetTextColor")
	pSetBkMode         = gdi32.NewProc("SetBkMode")
	pSetBkColor        = gdi32.NewProc("SetBkColor")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pGlobalLock       = kernel32.NewProc("GlobalLock")
	pGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	pGlobalSize       = kernel32.NewProc("GlobalSize")
	pRtlMoveMemory    = kernel32.NewProc("RtlMoveMemory")
)

// Mensagens, estilos e constantes do Win32 usados aqui.
const (
	wmDestroy         = 0x0002
	wmSize            = 0x0005
	wmSetFocus        = 0x0007
	wmClose           = 0x0010
	wmPaint           = 0x000F
	wmEraseBkgnd      = 0x0014
	wmSetCursor       = 0x0020
	wmGetMinMaxInfo   = 0x0024
	wmSetFont         = 0x0030
	wmKeyDown         = 0x0100
	wmKeyUp           = 0x0101
	wmSysKeyDown      = 0x0104
	wmSysKeyUp        = 0x0105
	wmCommand         = 0x0111
	wmCtlColorStatic  = 0x0138
	wmCtlColorEdit    = 0x0133
	wmCtlColorListbox = 0x0134
	wmMouseMove       = 0x0200
	wmLButtonDown     = 0x0201
	wmLButtonUp       = 0x0202
	wmRButtonDown     = 0x0204
	wmRButtonUp       = 0x0205
	wmMButtonDown     = 0x0207
	wmMButtonUp       = 0x0208
	wmMouseWheel      = 0x020A
	wmApp             = 0x8000

	wsOverlappedWindow  = 0x00CF0000
	wsPopup             = 0x80000000
	wsVisible           = 0x10000000
	wsChild             = 0x40000000
	wsTabStop           = 0x00010000
	wsBorder            = 0x00800000
	wsVScroll           = 0x00200000
	wsClipChildren      = 0x02000000
	esAutoHScroll       = 0x0080
	esPassword          = 0x0020
	bsDefPushButton     = 0x0001
	lbsNotify           = 0x0001
	lbsNoIntegralHeight = 0x0100
	ssLeft              = 0x0000

	swHide       = 0
	swShow       = 5
	swShowNormal = 1

	lbAddString    = 0x0180
	lbResetContent = 0x0184
	lbSetCurSel    = 0x0186
	lbGetCurSel    = 0x0188
	lbnDblClk      = 2
	bnClicked      = 0
	emSetCueBanner = 0x1501

	cwUseDefault = 0x80000000

	mfString    = 0x0000
	mfPopup     = 0x0010
	mfSeparator = 0x0800
	mfChecked   = 0x0008
	mfUnchecked = 0x0000
	mfByCommand = 0x0000

	whKeyboardLL  = 13
	llkhfExtended = 0x01
	llkhfInjected = 0x10
	llkhfUp       = 0x80

	cfUnicodeText = 13

	gwlStyle                = -16
	swpNoZOrder             = 0x0004
	swpFrameChanged         = 0x0020
	swpNoActivate           = 0x0010
	monitorDefaultToNearest = 2

	halftone    = 4
	dibRGB      = 0
	srcCopy     = 0x00CC0020
	transparent = 1

	dtCenter     = 0x0001
	dtVCenter    = 0x0004
	dtSingleLine = 0x0020
	dtWordBreak  = 0x0010

	htClient = 1
	idcArrow = 32512

	colorBg     = 0x00080402 // #020408 (BGR): o mesmo fundo do console
	colorPanel  = 0x00120A06
	colorText   = 0x00D8D2C7
	colorAccent = 0x00FFF000 // #00f0ff
	colorMuted  = 0x00786B5B
)

type point struct{ X, Y int32 }

type msg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	private uint32
}

type rect struct{ Left, Top, Right, Bottom int32 }

func (r rect) w() int32 { return r.Right - r.Left }
func (r rect) h() int32 { return r.Bottom - r.Top }

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type paintStruct struct {
	Hdc       windows.Handle
	Erase     int32
	Paint     rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type kbdLLHookStruct struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

type minMaxInfo struct {
	Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize point
}

func utf16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func loword(v uintptr) int32 { return int32(int16(v & 0xFFFF)) }
func hiword(v uintptr) int32 { return int32(int16((v >> 16) & 0xFFFF)) }

func setText(h windows.HWND, s string) {
	pSetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(utf16(s))))
}

func getText(h windows.HWND) string {
	n, _, _ := pGetWindowTextLengthW.Call(uintptr(h))
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func show(h windows.HWND, visible bool) {
	c := uintptr(swHide)
	if visible {
		c = swShow
	}
	pShowWindow.Call(uintptr(h), c)
}

func post(h windows.HWND, m uint32) { pPostMessageW.Call(uintptr(h), uintptr(m), 0, 0) }

func messageBox(owner windows.HWND, title, text string) {
	pMessageBoxW.Call(uintptr(owner), uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16(title))), 0x30)
}

// clipboardText lê o texto da área de transferência deste PC.
func clipboardText(owner windows.HWND) string {
	if r, _, _ := pOpenClipboard.Call(uintptr(owner)); r == 0 {
		return ""
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	size, _, _ := pGlobalSize.Call(h)
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 || size < 2 {
		return ""
	}
	defer pGlobalUnlock.Call(h)
	buf := make([]uint16, size/2)
	pRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), p, size)
	return syscall.UTF16ToString(buf)
}
