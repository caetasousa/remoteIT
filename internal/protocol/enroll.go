package protocol

// Espaço reservado no .exe do agente para o código de cadastro.
//
// O agente declara uma string de [EnrollSlotLen] bytes que começa com
// [EnrollMarker] e é completada com [EnrollPad]. No download, o relay acha o
// marcador no binário e grava o código do técnico logo depois dele, sem
// recompilar. Assim cada técnico recebe um .exe que cadastra as máquinas no
// nome dele (veja docs/adr/0010-maquinas-por-tecnico.md).
//
// O marcador só pode aparecer uma vez no binário: o agente nunca usa
// EnrollMarker sozinho em tempo de execução, só len(EnrollMarker), que é uma
// constante.
const (
	EnrollMarker = "~RemoteIT-enroll-slot-v1~"
	// EnrollCodeLen é o tamanho máximo do código gravado depois do marcador.
	EnrollCodeLen = 40
	// EnrollPad completa o espaço não usado pelo código.
	EnrollPad = '_'
	// EnrollEmpty é o conteúdo do espaço quando nenhum código foi gravado.
	EnrollEmpty = "________________________________________"
)
