package identity

import (
	"errors"
	"strings"
	"testing"
)

// Os fluxos com banco (login, sessão, remoção) são cobertos pelos testes de
// integração com Postgres real em test/integration.

func TestValidateNewUser(t *testing.T) {
	cases := []struct {
		name, user, pass string
		role             Role
		ok               bool
	}{
		{"válido", "maria.silva", "senha-forte", RoleTechnician, true},
		{"admin válido", "admin", "12345678", RoleAdmin, true},
		{"nome curto", "ab", "senha-forte", RoleAdmin, false},
		{"maiúscula", "Maria", "senha-forte", RoleAdmin, false},
		{"espaço", "maria silva", "senha-forte", RoleAdmin, false},
		{"senha curta", "maria", "1234567", RoleAdmin, false},
		{"senha acima do limite do bcrypt", "maria", strings.Repeat("x", 73), RoleAdmin, false},
		{"papel desconhecido", "maria", "senha-forte", Role("root"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateNewUser(c.user, c.pass, c.role)
			if c.ok && err != nil {
				t.Errorf("erro inesperado: %v", err)
			}
			if !c.ok && !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestDeleteSelfIsRefused(t *testing.T) {
	s := NewService(nil) // a regra é checada antes de tocar no repositório
	if _, err := s.DeleteUser(t.Context(), User{ID: 7}, 7); err != ErrSelfDelete {
		t.Errorf("err = %v, want ErrSelfDelete", err)
	}
}
