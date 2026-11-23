// Package web embute no binário do relay o build do console (frontend/).
//
// O Vite grava o build em dist/ deste diretório porque go:embed não enxerga
// pastas acima do pacote. Sem o build (só o .gitkeep), o relay compila e
// serve uma página avisando que o frontend precisa ser gerado.
package web

import (
	"embed"
	"io/fs"
	"testing/fstest"
)

//go:embed all:dist
var dist embed.FS

const missing = `<!DOCTYPE html><meta charset="utf-8"><title>RemoteIT</title>
<p>Console não compilado. Rode <code>make frontend</code> e recompile o relay.</p>`

// FS devolve os arquivos do console com index.html na raiz.
func FS() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return fstest.MapFS{"index.html": {Data: []byte(missing)}}
	}
	return sub
}
