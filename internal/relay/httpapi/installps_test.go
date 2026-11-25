package httpapi

import (
	"strings"
	"testing"
)

func TestInstallPSQuotesAndEdition(t *testing.T) {
	svc, err := installPS(installPSData{ExeURL: "https://x/i/AB'C", SHA256: "ab12", Name: "suporte-ti-servico.exe", Service: true})
	if err != nil {
		t.Fatal(err)
	}
	// aspa simples dobrada: o valor não escapa da string do PowerShell
	if !strings.Contains(svc, `$url = 'https://x/i/AB''C'`) {
		t.Errorf("URL sem escape:\n%s", svc)
	}
	for _, want := range []string{`$sha = 'ab12'`, `-ArgumentList 'install' -Verb RunAs -Wait -PassThru`, "& {"} {
		if !strings.Contains(svc, want) {
			t.Errorf("serviço sem %q", want)
		}
	}
	// "exit" fecharia a janela do PowerShell do cliente; o script usa return
	if strings.Contains(svc, "LOCALAPPDATA") || strings.Contains(svc, "exit") {
		t.Error("o script de serviço não pode ter o ramo portátil nem fechar o PowerShell")
	}
	port, _ := installPS(installPSData{ExeURL: "https://x/i/C", SHA256: "cd", Name: "suporte-ti.exe"})
	if !strings.Contains(port, "LOCALAPPDATA") || strings.Contains(port, "RunAs") {
		t.Errorf("portátil:\n%s", port)
	}
}
