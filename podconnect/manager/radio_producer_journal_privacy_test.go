package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRadioProducerJournalRefusesUnsafeExistingStorageUnchanged(t *testing.T) {
	for _, scenario := range []string{"public_directory", "public_file", "shared_inode"} {
		t.Run(scenario, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(root, "ledger")
			journal, _, err := openRadioProducerJournal(directory)
			if err != nil {
				t.Fatal(err)
			}
			record := radioProducerRecord{Fence: radioFence{Token: "closed", ActionID: "closed", Generation: 1}, Terminal: true}
			if err = journal.write(record); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, producerRecordName(record.Fence.Token))
			switch scenario {
			case "public_directory":
				err = os.Chmod(directory, 0755)
			case "public_file":
				err = os.Chmod(path, 0644)
			case "shared_inode":
				err = os.Link(path, filepath.Join(root, "external-original"))
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			beforeFile, _ := os.Stat(path)
			beforeDir, _ := os.Stat(directory)
			if _, _, err = openRadioProducerJournal(directory); err == nil {
				t.Fatal("unsafe receipt storage accepted")
			}
			after, readErr := os.ReadFile(path)
			afterFile, _ := os.Stat(path)
			afterDir, _ := os.Stat(directory)
			if readErr != nil || string(after) != string(before) || !os.SameFile(beforeFile, afterFile) || beforeFile.Mode() != afterFile.Mode() || beforeDir.Mode() != afterDir.Mode() {
				t.Fatal("refusal changed existing ledger")
			}
		})
	}
}

type radioJournalForeignInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (f radioJournalForeignInfo) Sys() any { return &f.stat }
func TestRadioProducerJournalRejectsForeignManagerUID(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "receipt.json")
	if err := os.WriteFile(file, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	original := *info.Sys().(*syscall.Stat_t)
	if !producerJournalPrivateInfo(info, false) {
		t.Fatal("private real inode rejected")
	}
	original.Uid = uint32(os.Geteuid()) + 1
	if producerJournalPrivateInfo(radioJournalForeignInfo{info, original}, false) {
		t.Fatal("foreign uid accepted as manager receipt")
	}
}
