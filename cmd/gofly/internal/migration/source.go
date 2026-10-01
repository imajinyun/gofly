package migration

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/golang-migrate/migrate/v4/source"
)

// snapshotSource prevents a file edit between validation and execution from
// changing the SQL that corresponds to the recorded checksum.
type snapshotSource struct{ entries []Entry }

func (s snapshotSource) Open(string) (source.Driver, error) {
	return nil, errors.New("migration sources must be loaded from a validated local snapshot")
}
func (s snapshotSource) Close() error { return nil }
func (s snapshotSource) First() (uint, error) {
	if len(s.entries) == 0 {
		return 0, os.ErrNotExist
	}
	return uint(s.entries[0].Version), nil
}
func (s snapshotSource) Prev(version uint) (uint, error) {
	for i := len(s.entries) - 1; i >= 0; i-- {
		if uint(s.entries[i].Version) < version {
			return uint(s.entries[i].Version), nil
		}
	}
	return 0, os.ErrNotExist
}
func (s snapshotSource) Next(version uint) (uint, error) {
	for _, entry := range s.entries {
		if uint(entry.Version) > version {
			return uint(entry.Version), nil
		}
	}
	return 0, os.ErrNotExist
}
func (s snapshotSource) ReadUp(version uint) (io.ReadCloser, string, error) {
	return s.read(version, true)
}
func (s snapshotSource) ReadDown(version uint) (io.ReadCloser, string, error) {
	return s.read(version, false)
}
func (s snapshotSource) read(version uint, up bool) (io.ReadCloser, string, error) {
	for _, entry := range s.entries {
		if uint(entry.Version) == version {
			for _, body := range [][]byte{entry.upSQL, entry.downSQL} {
				tokens, err := sqlTokens(string(body))
				if err != nil {
					return nil, "", err
				}
				if len(tokens) == 0 {
					return nil, "", errors.New("both migration directions must contain SQL before execution")
				}
				if err := checkTransactions(tokens); err != nil {
					return nil, "", err
				}
			}
			data := entry.downSQL
			if up {
				data = entry.upSQL
			}
			return io.NopCloser(bytes.NewReader(data)), entry.Name, nil
		}
	}
	return nil, "", os.ErrNotExist
}

func checkTransactions(tokens []string) error {
	open := false
	for len(tokens) > 0 {
		end := 0
		for end < len(tokens) && tokens[end] != ";" {
			end++
		}
		statement := tokens[:end]
		if end == len(tokens) {
			tokens = nil
		} else {
			tokens = tokens[end+1:]
		}
		if len(statement) == 0 {
			continue
		}
		head := strings.ToUpper(statement[0])
		switch head {
		case "XA", "PREPARE", "EXECUTE", "CALL":
			return errors.New("indirect or prepared transaction execution is unsupported in migrations")
		case "BEGIN", "START":
			if open {
				return errors.New("nested transaction control is unsupported")
			}
			open = true
		case "COMMIT", "END", "ROLLBACK", "ABORT":
			if head == "ROLLBACK" && len(statement) > 1 && strings.EqualFold(statement[1], "TO") {
				continue
			}
			for _, token := range statement[1:] {
				if !strings.EqualFold(token, "WORK") && !strings.EqualFold(token, "TRANSACTION") {
					return errors.New("chained or prepared transaction control is unsupported")
				}
			}
			open = false
		case "SET":
			for _, token := range statement[1:] {
				if strings.Contains(strings.ToLower(token), "autocommit") {
					return errors.New("session autocommit changes are unsupported in migrations")
				}
			}
		}
	}
	if open {
		return errors.New("migration has an unclosed transaction; add COMMIT or ROLLBACK")
	}
	return nil
}
