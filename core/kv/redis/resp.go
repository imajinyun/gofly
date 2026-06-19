// Package redis provides a minimal RESP2 Redis client with connection pooling.
package redis

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// ErrNil is returned when Redis replies with a nil bulk string or array.
var ErrNil = errors.New("redis: nil reply")

// reply represents a decoded RESP2 value. Only the fields relevant to the
// decoded type are populated.
type reply struct {
	kind    byte
	str     []byte
	integer int64
	array   []reply
	isNil   bool
}

// writeCommand encodes a command as a RESP2 array of bulk strings.
func writeCommand(w *bufio.Writer, args ...string) error {
	if len(args) == 0 {
		return errors.New("redis: empty command")
	}
	if _, err := fmt.Fprintf(w, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(w, "$%d\r\n", len(arg)); err != nil {
			return err
		}
		if _, err := w.WriteString(arg); err != nil {
			return err
		}
		if _, err := w.WriteString("\r\n"); err != nil {
			return err
		}
	}
	return w.Flush()
}

// readReply decodes a single RESP2 reply from r.
func readReply(r *bufio.Reader) (reply, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return reply{}, err
	}
	line, err := readLine(r)
	if err != nil {
		return reply{}, err
	}
	switch prefix {
	case '+':
		return reply{kind: prefix, str: line}, nil
	case '-':
		return reply{}, &Error{Message: string(line)}
	case ':':
		n, err := strconv.ParseInt(string(line), 10, 64)
		if err != nil {
			return reply{}, fmt.Errorf("redis: invalid integer %q: %w", line, err)
		}
		return reply{kind: prefix, integer: n}, nil
	case '$':
		length, err := strconv.Atoi(string(line))
		if err != nil {
			return reply{}, fmt.Errorf("redis: invalid bulk length %q: %w", line, err)
		}
		if length < 0 {
			return reply{kind: prefix, isNil: true}, nil
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return reply{}, err
		}
		if buf[length] != '\r' || buf[length+1] != '\n' {
			return reply{}, errors.New("redis: invalid bulk terminator")
		}
		return reply{kind: prefix, str: buf[:length]}, nil
	case '*':
		count, err := strconv.Atoi(string(line))
		if err != nil {
			return reply{}, fmt.Errorf("redis: invalid array length %q: %w", line, err)
		}
		if count < 0 {
			return reply{kind: prefix, isNil: true}, nil
		}
		items := make([]reply, count)
		for i := 0; i < count; i++ {
			item, err := readReply(r)
			if err != nil {
				return reply{}, err
			}
			items[i] = item
		}
		return reply{kind: prefix, array: items}, nil
	default:
		return reply{}, fmt.Errorf("redis: unknown reply type %q", prefix)
	}
}

// readLine reads up to a trailing CRLF and returns the content without it.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	n := len(line)
	if n >= 2 && line[n-2] == '\r' {
		return line[:n-2], nil
	}
	return line[:n-1], nil
}

// Error represents an error reply returned by the Redis server.
type Error struct {
	Message string
}

func (e *Error) Error() string { return "redis: " + e.Message }

func (r reply) bytes() ([]byte, error) {
	if r.isNil {
		return nil, ErrNil
	}
	switch r.kind {
	case '$', '+':
		return r.str, nil
	case ':':
		return []byte(strconv.FormatInt(r.integer, 10)), nil
	default:
		return nil, fmt.Errorf("redis: unexpected reply type %q", r.kind)
	}
}

func (r reply) int64() (int64, error) {
	if r.isNil {
		return 0, ErrNil
	}
	switch r.kind {
	case ':':
		return r.integer, nil
	case '$', '+':
		return strconv.ParseInt(string(r.str), 10, 64)
	default:
		return 0, fmt.Errorf("redis: unexpected reply type %q", r.kind)
	}
}

func (r reply) status() (string, error) {
	if r.isNil {
		return "", ErrNil
	}
	switch r.kind {
	case '+', '$':
		return string(r.str), nil
	default:
		return "", fmt.Errorf("redis: unexpected reply type %q", r.kind)
	}
}
