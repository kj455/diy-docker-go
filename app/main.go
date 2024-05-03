package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"syscall"
)

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	// create empty dir for chroot
	dir, err := os.MkdirTemp("", "docker")
	if err != nil {
		fmt.Printf("Err: %v", err)
		os.Exit(1)
	}

	// copy binary to chroot
	err = copyFile(command, path.Join(dir, command))
	if err != nil {
		fmt.Printf("copy file: %v", err)
		os.Exit(1)
	}

	// make dev/null
	err = os.MkdirAll(path.Join(dir, "dev"), 0755)
	if err != nil {
		fmt.Printf("mkdir: %v", err)
		os.Exit(1)
	}

	// chroot
	err = syscall.Chroot(dir)
	if err != nil {
		fmt.Printf("chroot: %v", err)
		os.Exit(1)
	}
	
	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		fmt.Printf("Err: %v", err)
		os.Exit(cmd.ProcessState.ExitCode())
	}	
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
