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
	"path"
	"log"
)

func mode(jsmode int) int {
	if i32, err := strconv.ParseInt("777", 8, 32); err == nil {
		if result, err := strconv.ParseInt(strconv.FormatInt(int64(jsmode) & i32, 8), 10, 32); err == nil {
			return int(result)
		}
	}
	return 0
}

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

type StoreType string
const StoreBlob StoreType = "0"
const StoreContent StoreType = "1"
const StoreLinks StoreType = "2"
const StoreStat StoreType = "3"

type FileStat struct {
	Mode int
	Size int64
	IsFileValue bool
	IsDirectoryValue bool
	IsSocketValue bool
	IsSymbolicLinkValue bool
}

type PkgFS struct {
	*os.File
	rd *bufio.Reader
	PayloadPosition int64
	PayloadSize int64
	PreludePosition int64
	PreludeSize int64
	VirtualFS map[string]map[StoreType][]int64
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

func (it *PkgFS) GetStat(vpath string) (*FileStat, error) {
	f, ok := it.VirtualFS[vpath]
	if !ok {
		return nil, fmt.Errorf("vpath not found: %s", vpath)
	}
	pos := f[StoreStat][0]
	size := f[StoreStat][1]
	if err := it.ResetReader(it.PayloadPosition+pos); err != nil {
		return nil, fmt.Errorf("reset header err: %w", err)
	}
	bs, err := it.rd.Peek(int(size))
	if err != nil {
		return nil, fmt.Errorf("peek err: %w", err)
	}
	var stat FileStat
	if err := json.Unmarshal(bs, &stat); err != nil {
		return nil, fmt.Errorf("json.unmarshal err: %w", err)
	}
	return &stat, nil
}

func (it *PkgFS) GetFile(vpath string, wr io.Writer) error {
	f, ok := it.VirtualFS[vpath]
	if !ok {
		return fmt.Errorf("vpath not found: %s", vpath)
	}
	var pos int64
	var size int64
	if blob, ok := f[StoreBlob]; ok {
		pos = blob[0]
		size = blob[1]
	} else if content, ok := f[StoreContent]; ok {
		pos = content[0]
		size = content[1]
	} else {
		return fmt.Errorf("no blob/content found in vpath %s", vpath)
	}
	if err := it.ResetReader(it.PayloadPosition+pos); err != nil {
		return fmt.Errorf("reset reader err: %w", err)
	}
	if _, err := io.CopyN(wr, it.rd, size); err != nil {
		return fmt.Errorf("io.copyn err: %w", err)
	}
	return nil
}

func (it *PkgFS) WriteFile(mountPoint string, vpath string) error {
	fpath := path.Join(mountPoint, vpath)
	os.MkdirAll(path.Dir(fpath), 0700)
	f, err := os.Create(fpath)
	if err != nil {
		return fmt.Errorf("os.create(%s) err: %w", fpath, err)
	}
	if err := it.GetFile(vpath, f); err != nil {
		return fmt.Errorf("GetFile err: %w", err)
	}
	return nil
}

func (it *PkgFS) WriteAll(mountPoint string) {
	total := len(it.VirtualFS)
	i := 0
	for vpath := range it.VirtualFS {
		stat, err := it.GetStat(vpath)
		if err == nil {
			if stat.IsFileValue {
				if err := it.WriteFile(mountPoint, vpath); err != nil {
					log.Println("write %s err: ", err)
				}
			}
		} else {
			log.Println("GetStat(%s) err: ", vpath, err)
		}
		fmt.Printf("\rProgress: %d/%d", i, total)
		i++
	}
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
	mountPoint := "d:\\project\\temp\\retool\\retool_backend_unpkg"
	fs.WriteAll(mountPoint)
}
