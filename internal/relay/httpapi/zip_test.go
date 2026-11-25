package httpapi

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestZipExeHasExeAndReadme(t *testing.T) {
	exe := bytes.Repeat([]byte("MZ-agente-"), 1000)
	z, err := zipExe("suporte-ti-servico.exe", exe)
	if err != nil {
		t.Fatal(err)
	}
	if len(z) >= len(exe) {
		t.Errorf("zip de %d bytes não comprimiu %d", len(z), len(exe))
	}
	r, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range r.File {
		rc, _ := f.Open()
		files[f.Name], _ = io.ReadAll(rc)
		rc.Close()
	}
	if !bytes.Equal(files["suporte-ti-servico.exe"], exe) {
		t.Error("o .exe dentro do zip não é o original")
	}
	readme := string(files["LEIA-ME.txt"])
	if !strings.Contains(readme, "Extraia") || !strings.Contains(readme, "\r\n") {
		t.Errorf("LEIA-ME sem instrução ou sem CRLF: %q", readme)
	}
}
