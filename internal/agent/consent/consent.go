// Package consent pergunta ao usuário da máquina se ele permite o acesso
// remoto. Nenhum quadro é enviado e nenhum input é aceito antes do "sim".
package consent

// Asker pergunta ao usuário se o acesso à máquina host é permitido e bloqueia
// até a resposta.
type Asker func(host string) bool

// Always é o Asker que permite sempre. Usado com -noconsent (servidores sem
// usuário) e fora do Windows.
func Always(string) bool { return true }
