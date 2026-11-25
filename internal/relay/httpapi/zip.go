package httpapi

import (
	"archive/zip"
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Downloads pelo navegador vão em .zip: o .exe solto é o que os navegadores
// mais seguram ("não é baixado com frequência", "pode ser perigoso") e
// renomeiam ("(1).exe"). Dentro do zip vão o .exe e um LEIA-ME. As rotas
// .exe continuam para curl, para a atualização automática e para o comando
// do PowerShell, que confere o hash do .exe cru.

// Textos do LEIA-ME de cada download (CRLF: abre certo no Bloco de Notas).
var readmes = map[string]string{
	"suporte-ti.exe": `RemoteIT - agente de suporte remoto (portátil)

1. Extraia este arquivo (botão direito > Extrair tudo).
2. Dê dois cliques em suporte-ti.exe.
3. Se o Windows mostrar "O Windows protegeu o computador", clique em
   "Mais informações" e depois em "Executar assim mesmo".

O agente roda sem instalar nada e fica ativo até o PC ser desligado.
Nenhuma janela abre: o técnico vê o PC na lista dele em alguns segundos.
`,
	"suporte-ti-servico.exe": `RemoteIT - agente de suporte remoto (serviço)

1. Extraia este arquivo (botão direito > Extrair tudo).
2. Dê dois cliques em suporte-ti-servico.exe.
3. O Windows pede permissão de administrador: clique em "Sim".

O agente é instalado em C:\Program Files\RemoteIT e inicia junto com o
Windows, inclusive na tela de bloqueio. O arquivo extraído pode ser apagado
depois. Para remover: Painel de Controle > Serviços, ou peça ao técnico.
`,
	"remoteit-tecnico.exe": `RemoteIT - visualizador do técnico (Windows)

1. Extraia este arquivo (botão direito > Extrair tudo) onde quiser,
   por exemplo na Área de Trabalho.
2. Dê dois cliques em remoteit-tecnico.exe e entre com a mesma conta do
   console. O endereço do relay já vem preenchido.

Ele se atualiza sozinho. Guia: docs/VISUALIZADOR.md no repositório.
`,
}

// zipExe monta um .zip com o .exe (comprimido) e o LEIA-ME.txt dele.
func zipExe(name string, exe []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	now := time.Now()
	add := func(n string, data []byte) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	if err := add(name, exe); err != nil {
		return nil, err
	}
	if txt, ok := readmes[name]; ok {
		if err := add("LEIA-ME.txt", []byte(strings.ReplaceAll(txt, "\n", "\r\n"))); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeDownload envia data como anexo. asZip embrulha o .exe num .zip com
// o mesmo nome (suporte-ti.exe → suporte-ti.zip).
func (s *server) writeDownload(w http.ResponseWriter, name string, data []byte, asZip bool) {
	ctype := "application/vnd.microsoft.portable-executable"
	if asZip {
		z, err := zipExe(name, data)
		if err != nil {
			s.Logger.Error("montar zip", "name", name, "err", err)
			writeError(w, http.StatusInternalServerError, "erro ao montar o .zip")
			return
		}
		data, ctype, name = z, "application/zip", strings.TrimSuffix(name, ".exe")+".zip"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	// Sem isso o Cloudflare (que guarda .exe por padrão) e o navegador
	// entregariam uma cópia antiga; private impede que um cache
	// compartilhado guarde um arquivo que cadastra máquinas no nome de alguém.
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}
