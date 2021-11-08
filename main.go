package main

import (
	"fmt"
	"bufio"
	"bytes"
	"os"
	"io"
	"strings"
	"strconv"
	"encoding/json"
)

func search(rd *bufio.Reader, pattern string) error {
	for {
		bs, err := rd.Peek(len(pattern))
		if err != nil {
			return fmt.Errorf("peek err: %w", err)
		}
		if !bytes.Equal([]byte(pattern), bs) {
			rd.ReadByte()
			continue
		}
		if _, err := rd.Discard(len(pattern)); err != nil {
			return fmt.Errorf("discard err: %w", err)
		}
		return nil
	}

}

func getPredefinedVariable(rd *bufio.Reader, name string) (string, error) {
	prefix := fmt.Sprintf("var %s = '", name)
	if err := search(rd, prefix); err != nil {
		return "", err
	}
	data, err := rd.ReadSlice('\'')
	if err != nil {
		return "", fmt.Errorf("read slice err: %w", err)
	}
	result := strings.TrimSpace(string(data[:len(data)-1]))
	return result, nil
}

func getPredefinedInt64(rd *bufio.Reader, name string) (int64, error) {
	result, err := getPredefinedVariable(rd, name)
	if err != nil {
		return 0, err
	}
	i64, err := strconv.ParseInt(result, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse int64 err: %w", err)
	}
	return i64, nil
}

func readBracket(rd *bufio.Reader, left string, right string) ([]byte, error) {
	var result bytes.Buffer
	wr := bufio.NewWriter(&result)
	stack := make([]struct{}, 0, 1024)
	push := func() {
		stack = append(stack, struct{}{})
	}
	pop := func() {
		stack = stack[:len(stack)-1]
	}
	for {
		bs, err := rd.Peek(len(left))
		if err != nil {
			return nil, fmt.Errorf("peek err: %w", err)
		}
		if bytes.Equal(bs, []byte(left)) {
			if _, err := io.CopyN(wr, rd, int64(len(left))); err != nil {
				return nil, fmt.Errorf("copyn err: %w", err)
			}
			push()
			break
		}
		rd.ReadByte()
	}
	for {
		bs, err := rd.Peek(len(left))
		if err != nil {
			return nil, fmt.Errorf("peek err: %w", err)
		}
		if bytes.Equal(bs, []byte(left)) {
			if _, err := io.CopyN(wr, rd, int64(len(left))); err != nil {
				return nil, fmt.Errorf("copyn err: %w", err)
			}
			push()
			continue
		}
		bs, err = rd.Peek(len(right))
		if bytes.Equal(bs, []byte(right)) {
			if _, err := io.CopyN(wr, rd, int64(len(right))); err != nil {
				return nil, fmt.Errorf("copyn err: %w", err)
			}
			pop()
			if len(stack) == 0 {
				break
			}
			continue
		}
		if _, err := io.CopyN(wr, rd, 1); err != nil {
			return nil, fmt.Errorf("copyn err: %w", err)
		}
	}
	wr.Flush()
	return result.Bytes(), nil
}

type PkgFS struct {
	*os.File
	rd *bufio.Reader
	PayloadPosition int64
	PayloadSize int64
	PreludePosition int64
	PreludeSize int64
	VirtualFS map[string]map[string][]int64
	Entrypoint string
}

func (it *PkgFS) ResetReader(offset int64) error {
	_, err := it.File.Seek(offset, io.SeekStart)
	if err != nil {
		return err
	}
	it.rd.Reset(it.File)
	return nil
}

func (it *PkgFS) Initialize() error {
	it.ResetReader(0)
	var err error
	it.PayloadPosition, err = getPredefinedInt64(it.rd, "PAYLOAD_POSITION")
	if err != nil {
		return fmt.Errorf("get PAYLOAD_POSITION err: %w", err)
	}
	it.PayloadSize, err = getPredefinedInt64(it.rd, "PAYLOAD_SIZE")
	if err != nil {
		return fmt.Errorf("get PAYLOAD_SIZE err: %w", err)
	}
	it.PreludePosition, err = getPredefinedInt64(it.rd, "PRELUDE_POSITION")
	if err != nil {
		return fmt.Errorf("get PRELUDE_POSITION err: %w", err)
	}
	it.PreludeSize, err = getPredefinedInt64(it.rd, "PRELUDE_SIZE")
	if err != nil {
		return fmt.Errorf("get PRELUDE_SIZE err: %w", err)
	}

	it.ResetReader(it.PreludePosition)

	for i := 0; i < 2; i++ {
		if err := search(it.rd, "function"); err != nil {
			return fmt.Errorf("search function err: %w", err)
		}
	}
	if _, err := readBracket(it.rd, "{", "}"); err != nil {
		return fmt.Errorf("readBracket body err: %w", err)
	}
	if _, err := readBracket(it.rd, "{", "}"); err != nil {
		return fmt.Errorf("readBracket arg1 err: %w", err)
	}
	it.rd.ReadBytes('\n')
	vfs, err := it.rd.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("ReadBytes vfs err: %w", err)
	}
	if err := json.Unmarshal(vfs, &it.VirtualFS); err != nil {
		return fmt.Errorf("unmarshal vfs err: %w", err)
	}
	it.rd.ReadBytes('\n')
	ep, err := it.rd.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read entrypoint err: %w", err)
	}
	it.Entrypoint = strings.Trim(string(ep), "\n\"")
	return nil
}

func NewPkgFS(path string) (*PkgFS, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &PkgFS{File: f, rd: bufio.NewReader(f)}, nil
}

func main() {
	fs, err := NewPkgFS("d:\\temp\\retool\\retool_backend")
	if err != nil {
		panic(err)
	}
	if err := fs.Initialize(); err != nil {
		panic(err)
	}
	fmt.Printf("%+v\n", fs.VirtualFS[fs.Entrypoint])
}
