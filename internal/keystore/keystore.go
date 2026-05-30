// Package keystore stores named OAuth tokens in an age-encrypted file under
// ~/.void-code/, unlocked by a per-install machine key (no passphrase).
//
// Threat model: the machine key (~/.void-code/.mk) protects against keys.age
// being copied off-machine. It does NOT defend against local disk theft — an
// accepted trade-off for zero-friction unlock on teaching clients' machines.
package keystore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"filippo.io/age"
)

// dirFn returns the directory holding .mk and keys.age. Overridable in tests.
var dirFn = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".void-code"), nil
}

func mkPath() (string, error) {
	dir, err := dirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".mk"), nil
}

func keysPath() (string, error) {
	dir, err := dirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "keys.age"), nil
}

// machineIdentity loads the per-install age identity from ~/.void-code/.mk,
// generating + persisting one (mode 0600) on first use.
func machineIdentity() (*age.X25519Identity, error) {
	p, err := mkPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err == nil {
		id, perr := age.ParseX25519Identity(string(bytes.TrimSpace(data)))
		if perr != nil {
			return nil, fmt.Errorf("parse machine key: %w", perr)
		}
		return id, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read machine key: %w", err)
	}
	// Generate + persist.
	id, gerr := age.GenerateX25519Identity()
	if gerr != nil {
		return nil, fmt.Errorf("generate machine key: %w", gerr)
	}
	dir := filepath.Dir(p)
	if mderr := os.MkdirAll(dir, 0o700); mderr != nil {
		return nil, fmt.Errorf("mkdir keystore dir: %w", mderr)
	}
	if werr := os.WriteFile(p, []byte(id.String()), 0o600); werr != nil {
		return nil, fmt.Errorf("write machine key: %w", werr)
	}
	return id, nil
}

// encrypt writes plaintext age-encrypted to the given writer using the machine recipient.
func encrypt(w io.Writer, id *age.X25519Identity, plaintext []byte) error {
	aw, err := age.Encrypt(w, id.Recipient())
	if err != nil {
		return err
	}
	if _, err := aw.Write(plaintext); err != nil {
		return err
	}
	return aw.Close()
}

// decrypt reads age-encrypted bytes from r using the machine identity.
func decrypt(r io.Reader, id *age.X25519Identity) ([]byte, error) {
	dr, err := age.Decrypt(r, id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(dr)
}

// loadMap decrypts keys.age into the name→token map. Absent file = empty map.
func loadMap() (map[string]string, *age.X25519Identity, error) {
	id, err := machineIdentity()
	if err != nil {
		return nil, nil, err
	}
	p, err := keysPath()
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return map[string]string{}, id, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open keys.age: %w", err)
	}
	defer f.Close()

	plain, err := decrypt(f, id)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt keys.age: %w", err)
	}
	m := map[string]string{}
	if len(plain) > 0 {
		if err := json.Unmarshal(plain, &m); err != nil {
			return nil, nil, fmt.Errorf("parse keys.age json: %w", err)
		}
	}
	return m, id, nil
}

// saveMap encrypts m and writes keys.age atomically (mode 0600).
func saveMap(m map[string]string, id *age.X25519Identity) error {
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := encrypt(&buf, id, plain); err != nil {
		return fmt.Errorf("encrypt keys.age: %w", err)
	}
	p, err := keysPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, buf.Bytes(), 0o600)
}

// AddKey saves (or overwrites) the named OAuth token.
func AddKey(name, token string) error {
	if name == "" {
		return fmt.Errorf("key name must not be empty")
	}
	if token == "" {
		return fmt.Errorf("token must not be empty")
	}
	m, id, err := loadMap()
	if err != nil {
		return err
	}
	m[name] = token
	return saveMap(m, id)
}

// ListKeys returns the saved key names, sorted.
func ListKeys() ([]string, error) {
	m, _, err := loadMap()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

// GetKey returns the token for name, or an error if absent.
func GetKey(name string) (string, error) {
	m, _, err := loadMap()
	if err != nil {
		return "", err
	}
	tok, ok := m[name]
	if !ok {
		return "", fmt.Errorf("no saved key named %q", name)
	}
	return tok, nil
}

// DeleteKey removes the named key. Absent name is a no-op (no error).
func DeleteKey(name string) error {
	m, id, err := loadMap()
	if err != nil {
		return err
	}
	delete(m, name)
	return saveMap(m, id)
}
