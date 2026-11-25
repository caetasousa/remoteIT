package machine

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// FileStore guarda a credencial num arquivo, protegido pelo DPAPI no
// Windows. MachineScope protege para a máquina (o worker do serviço, que
// roda como SYSTEM); sem ele, para o usuário atual (o agente portátil).
type FileStore struct {
	Path         string
	MachineScope bool
}

// Load lê a credencial; false se não existe ou não pode ser lida.
func (f FileStore) Load() (Credential, bool) {
	b, err := os.ReadFile(f.Path)
	if err != nil {
		return Credential{}, false
	}
	if b, err = unprotect(b, f.MachineScope); err != nil {
		return Credential{}, false
	}
	var c Credential
	if json.Unmarshal(b, &c) != nil || c.MachineID == "" || c.Secret == "" {
		return Credential{}, false
	}
	return c, true
}

// Save grava a credencial, substituindo a anterior de uma vez só.
func (f FileStore) Save(c Credential) error {
	b, _ := json.Marshal(c)
	b, err := protect(b, f.MachineScope)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	tmp := f.Path + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

// Clear apaga a credencial.
func (f FileStore) Clear() error {
	if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
