package httpapi

import (
	"strings"
	"text/template"
)

// installPSData preenche o script de instalação por PowerShell.
type installPSData struct {
	ExeURL  string // o mesmo link de instalação, que serve o .exe marcado
	SHA256  string // hash do .exe que esse link serve agora
	Name    string // nome do arquivo (suporte-ti.exe ou suporte-ti-servico.exe)
	Service bool   // edição de serviço: instala com UAC; senão, roda portátil
}

// psQuote escapa s para uma string PowerShell entre aspas simples, onde só
// a própria aspa simples é especial (vira duas).
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// O script roda dentro de "& { ... }": as variáveis e o $ProgressPreference
// ficam só nele, e "return" encerra sem fechar a janela do PowerShell (o
// "exit" fecharia). Funciona no Windows PowerShell 5.1, que vem com o
// Windows 10 e 11. "irm | iex" não passa pela política de execução de
// scripts: o texto é executado como comando digitado.
var installPSTmpl = template.Must(template.New("install.ps1").Funcs(template.FuncMap{"q": psQuote}).Parse(`# RemoteIT: instala o agente de suporte remoto deste link.
# Uso (PowerShell): irm <link>/ps | iex
& {
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue' # a barra de progresso deixa o download muito mais lento
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

  $url = {{q .ExeURL}}
  $sha = {{q .SHA256}}
  $dir = Join-Path $env:TEMP ('RemoteIT-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  $exe = Join-Path $dir {{q .Name}}

  Write-Host 'RemoteIT: baixando o agente...' -ForegroundColor Cyan
  try {
    Invoke-WebRequest -Uri $url -OutFile $exe -UseBasicParsing
  } catch {
    Write-Host ('Não foi possível baixar o agente: ' + $_.Exception.Message) -ForegroundColor Red
    return
  }
  $got = (Get-FileHash -Algorithm SHA256 -Path $exe).Hash.ToLowerInvariant()
  if ($got -ne $sha) {
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host 'O arquivo baixado não confere com o esperado (SHA-256). Nada foi instalado.' -ForegroundColor Red
    return
  }
{{- if .Service}}

  Write-Host 'RemoteIT: instalando o serviço (o Windows vai pedir permissão de administrador)...' -ForegroundColor Cyan
  try {
    $p = Start-Process -FilePath $exe -ArgumentList 'install' -Verb RunAs -Wait -PassThru
  } catch {
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host 'Instalação cancelada: a permissão de administrador não foi dada.' -ForegroundColor Yellow
    return
  }
  Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
  if ($p.ExitCode -ne 0) {
    Write-Host ('A instalação falhou (código ' + $p.ExitCode + '). Detalhes em C:\ProgramData\RemoteIT\service.log') -ForegroundColor Red
    return
  }
  Write-Host 'Pronto: o RemoteIT foi instalado como serviço e inicia junto com o Windows.' -ForegroundColor Green
{{- else}}

  $dest = Join-Path $env:LOCALAPPDATA 'RemoteIT'
  New-Item -ItemType Directory -Path $dest -Force | Out-Null
  $run = Join-Path $dest 'suporte-ti.exe'
  try {
    Copy-Item -Path $exe -Destination $run -Force
  } catch {
    Write-Host 'O RemoteIT já está rodando neste PC. Feche-o (ou reinicie o PC) e rode o comando de novo.' -ForegroundColor Yellow
    return
  } finally {
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
  }
  Start-Process -FilePath $run | Out-Null
  Write-Host 'Pronto: o RemoteIT está rodando e fica ativo até o PC ser desligado.' -ForegroundColor Green
{{- end}}
  Write-Host 'A máquina aparece para o técnico em alguns segundos.'
}
`))

// installPS monta o script de instalação.
func installPS(d installPSData) (string, error) {
	var b strings.Builder
	err := installPSTmpl.Execute(&b, d)
	return b.String(), err
}
