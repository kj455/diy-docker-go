//go:build linux
// +build linux

package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"syscall"
)

func chroot(command, dir string) error {
	err := copyFile(command, path.Join(dir, command))
	if err != nil {
		return fmt.Errorf("copy file: %v", err)
	}
	err = os.MkdirAll(path.Join(dir, "dev/null"), 0755)
	if err != nil {
		return fmt.Errorf("mkdir: %v", err)
	}
	err = syscall.Chroot(dir)
	if err != nil {
		return fmt.Errorf("chroot: %v", err)
	}
	return nil
}

func copyFile(src, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("create file: %v", err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %v", err)
	}
	mode := srcInfo.Mode()

	destDir := path.Dir(dest)
	err = os.MkdirAll(destDir, 0755)
	if err != nil {
		return fmt.Errorf("mkdir: %v", err)
	}

	destFile, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create file: %v", err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return fmt.Errorf("copy file: %v", err)
	}

	err = os.Chmod(dest, mode)
	if err != nil {
		return fmt.Errorf("chmod file: %v", err)
	}

	return nil
}
