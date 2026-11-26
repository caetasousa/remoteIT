package media

import (
	"image"
	"slices"
	"testing"
	"time"
)

func TestTileRectsMergeRowsAndColumns(t *testing.T) {
	// grade 4x3 de blocos de 10 px num quadro 35x25 (a última coluna e a
	// última linha são cortadas na borda)
	ts := newTileState(35, 25, 4, 3, 10)
	sel := map[int]bool{
		0: true, 1: true, // linha 0: colunas 0-1
		4: true, 5: true, // linha 1: colunas 0-1 (mesma faixa: emenda com a de cima)
		7:  true, // linha 1: coluna 3 (borda)
		11: true, // linha 2: coluna 3 (mesma faixa da de cima)
	}
	got := ts.rects(func(i int) bool { return sel[i] })
	want := []image.Rectangle{image.Rect(0, 0, 20, 20), image.Rect(30, 10, 35, 25)}
	if !slices.Equal(got, want) {
		t.Fatalf("rects = %v, quero %v", got, want)
	}
}

func TestTileRectsCapUsesBoundingBox(t *testing.T) {
	// tabuleiro de xadrez: mais retângulos que maxRects
	ts := newTileState(400, 400, 20, 20, 20)
	got := ts.rects(func(i int) bool { return (i%20+i/20)%2 == 0 })
	if len(got) != 1 || got[0] != image.Rect(0, 0, 400, 400) {
		t.Fatalf("com retângulos demais deveria devolver o que envolve todos: %v", got)
	}
}

func TestTileStateLifecycle(t *testing.T) {
	t0 := time.Unix(1000, 0)
	ts := newTileState(128, 64, 2, 1, 64)
	if ts.anyLossy() {
		t.Fatal("estado novo não deveria ter perda")
	}
	ts.markAll(t0)
	if !ts.allLossy() {
		t.Fatal("markAll deveria marcar a tela inteira")
	}
	ts.mark([]bool{true, false}, t0.Add(200*time.Millisecond))
	if got := ts.settled(t0.Add(350*time.Millisecond), StillDelay); !slices.Equal(got, []int{1}) {
		t.Fatalf("só o bloco 1 está parado há StillDelay: %v", got)
	}
	g := slices.Clone(ts.gen)
	ts.mark([]bool{false, true}, t0.Add(400*time.Millisecond))
	if ts.same([]int{1}, []uint32{g[1]}) {
		t.Fatal("o bloco 1 mudou: a captura dele ficou velha")
	}
	ts.clean([]int{1})
	if !ts.lossy[0] || ts.lossy[1] {
		t.Fatalf("clean só limpa o que recebeu retalho: %v", ts.lossy)
	}
	if got := flat(ts.lossyRects()); !slices.Equal(got, []int{0, 0, 64, 64}) {
		t.Fatalf("região com perda = %v", got)
	}
}
