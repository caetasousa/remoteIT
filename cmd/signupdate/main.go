// Command signupdate cuida da chave de atualização do agente e assina o
// manifesto que o relay serve aos agentes (docs/adr/0011-atualizacao-automatica.md).
//
// Uso (o build.sh chama sozinho):
//
//	signupdate -key arquivo -genkey            cria a chave, se ainda não existir
//	signupdate -key arquivo -pub               imprime a chave pública (base64)
//	signupdate -key arquivo -sign dist -version V
//	                                           grava dist/update.json assinando
//	                                           suporte-ti.exe, suporte-ti-servico.exe e,
//	                                           se existir, remoteit-tecnico.exe
//
// A chave privada nunca vai para o servidor: só o update.json, com as assinaturas.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"remoteit/internal/protocol"
)

func main() {
	keyPath := flag.String("key", "", "arquivo da chave privada (base64)")
	genkey := flag.Bool("genkey", false, "cria a chave se não existir")
	pub := flag.Bool("pub", false, "imprime a chave pública")
	dist := flag.String("sign", "", "pasta com os .exe para assinar")
	version := flag.String("version", "", "versão dos .exe")
	flag.Parse()
	if err := run(*keyPath, *genkey, *pub, *dist, *version); err != nil {
		fmt.Fprintln(os.Stderr, "signupdate:", err)
		os.Exit(1)
	}
}

func run(keyPath string, genkey, pub bool, dist, version string) error {
	if keyPath == "" {
		return errors.New("informe -key")
	}
	if genkey {
		if _, err := os.Stat(keyPath); err == nil {
			return nil
		}
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			return err
		}
		return os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600)
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	b, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return errors.New("chave inválida")
	}
	priv := ed25519.PrivateKey(b)
	if pub {
		fmt.Print(base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
		return nil
	}
	if dist == "" || version == "" {
		return errors.New("use -genkey, -pub ou -sign pasta -version V")
	}
	m := protocol.UpdateManifest{Version: version, Files: map[string]protocol.UpdateFile{}}
	for edition, name := range map[string]string{"portatil": "suporte-ti.exe", "servico": "suporte-ti-servico.exe", "tecnico": "remoteit-tecnico.exe"} {
		exe, err := os.ReadFile(filepath.Join(dist, name))
		if edition == "tecnico" && os.IsNotExist(err) {
			continue // build sem o visualizador nativo
		}
		if err != nil {
			return err
		}
		sum := sha256.Sum256(exe)
		hexsum := hex.EncodeToString(sum[:])
		m.Files[edition] = protocol.UpdateFile{
			SHA256:    hexsum,
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, protocol.UpdateMessage(edition, version, hexsum))),
			Size:      int64(len(exe)),
		}
	}
	out, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(filepath.Join(dist, "update.json"), out, 0o644)
}
