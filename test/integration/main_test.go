//go:build integration

// Package integration testa os componentes juntos e de verdade: relay HTTP
// real (httptest), PostgreSQL real (testcontainers), WebSocket real e, nos
// testes de agente, o pipeline real do agente com a tela sintética.
//
// Rodar: make test-integration  (ou go test -tags integration ./test/integration/...)
// Precisa de Docker. Com TEST_DATABASE_URL definida, usa esse banco em vez
// de subir um container.
package integration

import (
	"context"
	"fmt"
	"os"
	"testing"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// postgresImage deve acompanhar a versão do compose.yaml.
const postgresImage = "postgres:19-alpine"

var dsn string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if dsn = os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return m.Run()
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("remoteit"),
		tcpostgres.WithUsername("remoteit"),
		tcpostgres.WithPassword("remoteit"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "não foi possível subir o Postgres (Docker está rodando?):", err)
		return 1
	}
	defer ctr.Terminate(ctx)
	defer func() {
		if sharedMailpit.ctr != nil {
			sharedMailpit.ctr.Terminate(ctx)
		}
	}()
	if dsn, err = ctr.ConnectionString(ctx, "sslmode=disable"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}
