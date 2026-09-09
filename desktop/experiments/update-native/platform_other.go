//go:build !darwin && !windows

package main

import (
	"errors"
	"os"
)

func acquireLock(string) (*os.File, error) {
	return nil, errors.New("native probe supports only macOS and Windows")
}
func atomicReplace(string, string) error           { return errors.New("unsupported operating system") }
func processAlive(int) bool                        { return true }
func startFixture(string, string, []string) error  { return errors.New("unsupported operating system") }
func runNSIS(string, string, string, string) error { return errors.New("unsupported operating system") }
