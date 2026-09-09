//go:build darwin

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func acquireLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func atomicReplace(source, destination string) error { return os.Rename(source, destination) }

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func startFixture(executable, argument string, environment []string) error {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	command := exec.Command(executable, argument)
	command.Env = environment
	command.Stdin = null
	command.Stdout = null
	command.Stderr = null
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func runNSIS(installer, target, capsule, transaction string) error {
	return errors.New("NSIS is unavailable on macOS")
}
