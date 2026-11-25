// Package clipboard põe na área de transferência do Windows o texto que o
// técnico mandou (mensagem "clip"), para ele colar com Ctrl+V no PC remoto.
package clipboard

// Set substitui o conteúdo da área de transferência por text. Erros são
// ignorados: a área de transferência pode estar presa por outro programa, e
// o técnico simplesmente tenta de novo.
func Set(text string) { set(text) }
