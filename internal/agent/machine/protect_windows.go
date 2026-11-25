//go:build windows

package machine

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// protect cifra com o DPAPI: só esta máquina (machineScope) ou só este
// usuário nesta máquina consegue abrir. Copiar o arquivo para outro PC não
// leva a credencial junto.
func protect(b []byte, machineScope bool) ([]byte, error) {
	return dpapi(b, machineScope, true)
}

func unprotect(b []byte, machineScope bool) ([]byte, error) {
	return dpapi(b, machineScope, false)
}

func dpapi(b []byte, machineScope, encrypt bool) ([]byte, error) {
	if len(b) == 0 {
		return nil, windows.ERROR_INVALID_DATA
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	flags := uint32(windows.CRYPTPROTECT_UI_FORBIDDEN)
	if machineScope {
		flags |= windows.CRYPTPROTECT_LOCAL_MACHINE
	}
	var err error
	if encrypt {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, flags, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, flags, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
