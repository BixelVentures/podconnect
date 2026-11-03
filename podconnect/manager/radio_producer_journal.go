package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// All writes precede corresponding effects and fsync both inode and directory.
// Existing files are never truncated; atomic rename publishes a complete record.
// History is capped at 4096 records. There is no automatic pruning of active,
// unresolved or retained per-target holds; reaching the cap blocks new admission.
type radioProducerJournal struct{ directory string }

func producerRecordName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:]) + ".json"
}
func openRadioProducerJournal(directory string) (*radioProducerJournal, map[string]radioProducerRecord, error) {
	records := map[string]radioProducerRecord{}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, nil, errRadioUnavailable
	}
	for p := directory; p != filepath.Dir(p); p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, errRadioUnavailable
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, nil, e
		}
	}
	if e := os.MkdirAll(directory, 0700); e != nil {
		return nil, nil, e
	}
	directoryInfo, e := os.Lstat(directory)
	if e != nil || !producerJournalPrivateInfo(directoryInfo, true) {
		return nil, nil, errRadioUnavailable
	}
	entries, e := os.ReadDir(directory)
	if e != nil {
		return nil, nil, e
	}
	if len(entries) > 4096 {
		return nil, nil, errRadioUnavailable
	}
	for _, entry := range entries {
		info, statErr := entry.Info()
		if statErr != nil || !producerJournalPrivateInfo(info, false) || !info.Mode().IsRegular() || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, nil, errRadioUnknown
		}
		path := filepath.Join(directory, entry.Name())
		f, e := os.Open(path)
		if e != nil {
			return nil, nil, e
		}
		opened, statErr := f.Stat()
		if statErr != nil || !producerJournalPrivateInfo(opened, false) || !os.SameFile(info, opened) {
			f.Close()
			return nil, nil, errRadioUnknown
		}
		raw, e := io.ReadAll(io.LimitReader(f, 65537))
		f.Close()
		if e != nil || len(raw) > 65536 {
			return nil, nil, errRadioUnknown
		}
		var r radioProducerRecord
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if dec.Decode(&r) != nil || dec.Decode(new(any)) != io.EOF || r.Fence.Token == "" || r.Fence.ActionID == "" || r.Fence.Generation == 0 || producerRecordName(r.Fence.Token) != entry.Name() {
			return nil, nil, errRadioUnknown
		}
		records[r.Fence.Token] = r
	}
	return &radioProducerJournal{directory}, records, nil
}
func (j *radioProducerJournal) write(r radioProducerRecord) error {
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > 65536 {
		return errRadioUnknown
	}
	temp, e := os.CreateTemp(j.directory, ".radio-journal-")
	if e != nil {
		return e
	}
	name := temp.Name()
	defer os.Remove(name)
	if e = temp.Chmod(0600); e == nil {
		_, e = temp.Write(raw)
	}
	if e == nil {
		e = temp.Sync()
	}
	closeErr := temp.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(name, filepath.Join(j.directory, producerRecordName(r.Fence.Token))); e != nil {
		return e
	}
	dir, e := os.Open(j.directory)
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}

// A retained authenticated receipt is trusted only in the manager's private
// ledger. Refuse unsafe preexisting storage; never repair ownership/mode or
// consume a foreign/shared inode while deciding whether startup is safe.
func producerJournalPrivateInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode().Perm()&0077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode()&os.ModeSymlink == 0
	}
	return info.Mode().IsRegular() && stat.Nlink == 1
}
