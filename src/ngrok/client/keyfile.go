package client

import (
	"crypto/rand"
	"encoding/hex"
	"io/ioutil"
	"os"
	"path"
	"strings"
)

// MachineKey is the persistent per-machine credential used by zero-config
// agent mode. It is generated on first run and never changes afterwards
// (until explicitly rotated with -new-key). One key serves three roles:
//   - client authtoken towards ngrokd (server validates a list of keys)
//   - access-gateway code on the SSH tunnel ("AUTH <key>")
//   - the single secret printed in the remote manual for AI agents
//
// It lives in ~/.ngrok.d/machine.key (0600) — a credential file, not a
// configuration file: agent mode never reads any user-written config.

const machineKeyPrefix = "ngk-"

func machineKeyPath() string {
	return path.Join(defaultAgentDir(), "machine.key")
}

// FormatMachineKey renders 16 random bytes as ngk-xxxxxxxx-xxxxxxxx-xxxxxxxx-xxxxxxxx
func FormatMachineKey(raw []byte) string {
	h := hex.EncodeToString(raw)
	return machineKeyPrefix + h[0:8] + "-" + h[8:16] + "-" + h[16:24] + "-" + h[24:32]
}

func newMachineKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return FormatMachineKey(b)
}

// LoadOrCreateMachineKey returns the machine key, creating it on first run.
func LoadOrCreateMachineKey() (key string, created bool, err error) {
	p := machineKeyPath()
	if b, rerr := ioutil.ReadFile(p); rerr == nil {
		key = strings.TrimSpace(string(b))
		if strings.HasPrefix(key, machineKeyPrefix) {
			return key, false, nil
		}
		// corrupt/foreign content: fall through and regenerate
	}
	key = newMachineKey()
	if err = os.MkdirAll(path.Dir(p), 0700); err != nil {
		return "", false, err
	}
	if err = ioutil.WriteFile(p, []byte(key+"\n"), 0600); err != nil {
		return "", false, err
	}
	return key, true, nil
}

// RotateMachineKey replaces the machine key with a fresh one and returns
// (newKey, oldKey). Old keys stop working immediately for new connections.
func RotateMachineKey() (newKey, oldKey string, err error) {
	old := ""
	if b, rerr := ioutil.ReadFile(machineKeyPath()); rerr == nil {
		old = strings.TrimSpace(string(b))
	}
	newKey = newMachineKey()
	if err = os.MkdirAll(path.Dir(machineKeyPath()), 0700); err != nil {
		return "", old, err
	}
	if err = ioutil.WriteFile(machineKeyPath(), []byte(newKey+"\n"), 0600); err != nil {
		return "", old, err
	}
	return newKey, old, nil
}

// MachineKeyFingerprint returns a short human-checkable suffix of the key.
func MachineKeyFingerprint(key string) string {
	if len(key) < 4 {
		return ""
	}
	return key[len(key)-4:]
}