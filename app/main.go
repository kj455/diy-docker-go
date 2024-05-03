//go:build linux
// +build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	imageName, command,	args := parseArgs(os.Args[1:])
	dir, err := os.MkdirTemp("", "tmp")
	if err != nil {
		fmt.Printf("Err: %v", err)
		os.Exit(1)
	}
	imageClient := newDockerImageClient(imageName, dir)
	err = imageClient.Pull()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	err = chroot(command, dir)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID,
	}
	err = cmd.Run()
	if err != nil {
		fmt.Printf("Err: %v", err)
		os.Exit(cmd.ProcessState.ExitCode())
	}
}

func parseArgs(args []string) (string, string, []string) {
	imageName := args[1]
	command := args[2]
	args = args[3:]
	return imageName, command, args
}
