//go:build integration

package integration

import (
	"os"
	"testing"
)

func readMigration(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../internal/relay/storage/postgres/migrations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
