package avc444

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

// fixturePath é a referência que o teste do console usa para conferir a
// recomposição 4:4:4 em WebGL (frontend/src/lib/viewer/render.ts e
// frontend/e2e/render.spec.ts). Para regenerar depois de mudar o arranjo:
//
//	UPDATE_FIXTURES=1 go test ./internal/avc444 -run TestBrowserFixture
const fixturePath = "../../frontend/e2e/fixtures/avc444.json"

// i420 separa um NV12 contíguo em planos Y, U e V (o formato que o
// decodificador em software do navegador entrega).
func i420(nv12 []byte, w, h int) (y, u, v []byte) {
	y = nv12[:w*h]
	uv := nv12[w*h:]
	u, v = make([]byte, len(uv)/2), make([]byte, len(uv)/2)
	for i := range u {
		u[i], v[i] = uv[2*i], uv[2*i+1]
	}
	return y, u, v
}

func TestBrowserFixture(t *testing.T) {
	const w, h = 64, 32
	img := randomImage(w, h, 7)
	main, aux := Pack(img)
	full := make([]byte, w*h*4)
	Combine(main, aux, w, h, full)
	onlyMain := make([]byte, w*h*4)
	Combine(main, nil, w, h, onlyMain)
	b64 := base64.StdEncoding.EncodeToString
	my, mu, mv := i420(main, w, h)
	ay, au, av := i420(aux, w, h)
	fx := map[string]any{
		"w": w, "h": h,
		"main":     map[string]string{"y": b64(my), "u": b64(mu), "v": b64(mv)},
		"aux":      map[string]string{"y": b64(ay), "u": b64(au), "v": b64(av)},
		"bgra444":  b64(full),     // Combine com as duas visões
		"bgraMain": b64(onlyMain), // só a principal
	}
	got, _ := json.MarshalIndent(fx, "", " ")
	got = append(got, '\n')
	if os.Getenv("UPDATE_FIXTURES") == "1" {
		if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(fixturePath)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s desatualizado (ou ausente): rode UPDATE_FIXTURES=1 go test ./internal/avc444 -run TestBrowserFixture", fixturePath)
	}
}
