package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const primaryOwnerFenceEnv = "PODCONNECT_PRIMARY_OWNER_FENCE"

type primaryOwnerFence struct {
	file       *os.File
	generation string
}

// The primary children inherit the same open file description. A successor
// cannot acquire it until the Manager and both old children close their copies.
func openPrimaryOwnerFence(dir string) (*primaryOwnerFence, error) {
	parent, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !parent.IsDir() || parent.Mode().Perm()&0o022 != 0 || parent.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, errors.New("primary owner directory is not private to its owner")
	}
	path := filepath.Join(dir, ".podconnect-primary-owner")
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	fi, err := file.Stat()
	if err == nil && (!fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 ||
		fi.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || fi.Sys().(*syscall.Stat_t).Nlink != 1) {
		err = errors.New("primary owner fence is not an owner-only regular file")
	}
	if err == nil {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err == nil {
		var named os.FileInfo
		named, err = os.Lstat(path)
		if err == nil && !os.SameFile(fi, named) {
			err = errors.New("primary owner fence changed during acquisition")
		}
	}
	var generation [16]byte
	if err == nil {
		_, err = rand.Read(generation[:])
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &primaryOwnerFence{file: file, generation: hex.EncodeToString(generation[:])}, nil
}

func (f *primaryOwnerFence) bind(cmd *exec.Cmd) *exec.Cmd {
	if f != nil {
		cmd.ExtraFiles = []*os.File{f.file}
		cmd.Env = append(cmd.Environ(), primaryOwnerFenceEnv+"="+f.generation)
	}
	return cmd
}
