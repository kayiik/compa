package compute

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ChatMessage is one line of a Compita's direct chat with the user.
type ChatMessage struct {
	Role  string    `json:"role"` // "user" or "compita"
	Text  string    `json:"text"`
	Error bool      `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

func chatPath(home, compitaID string) (string, error) {
	return statePath(home, compitaID, "chat.jsonl")
}

// AppendChat adds a message to the Compita's chat log on the Compa host.
func AppendChat(home, compitaID string, m ChatMessage) error {
	p, err := chatPath(home, compitaID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// LoadChat returns up to the last limit messages.
func LoadChat(home, compitaID string, limit int) ([]ChatMessage, error) {
	p, err := chatPath(home, compitaID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return []ChatMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []ChatMessage{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m ChatMessage
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, sc.Err()
}
