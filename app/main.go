package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"syscall"
)

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	// 1. create a temp dir
	tempDirPath, err := ioutil.TempDir("", "")
	if err != nil {
		fmt.Printf("Err: %v", err)
		os.Exit(1)
	}

	// 2. copy binary being executed
	err = copyCommandExecutableToTempDir(tempDirPath, command)
	if err != nil {
		fmt.Printf("copyCommandExecutableToTempDir - Err: %v", err)
		os.Exit(1)
	}
	err = createDevNull(tempDirPath)
	if err != nil {
		fmt.Printf("createDevNull - Err: %v", err)
		os.Exit(1)
	}

	// 3. chroot to temp dir
	if err := syscall.Chroot(tempDirPath); err != nil {
		fmt.Printf("Chroot - Err: %v", err)
		os.Exit(1)
	}

	//fmt.Printf("Args: %v\n", os.Args)

	cmd := exec.Command(command, args...)
	//cmd.Stdin = os.Stdin // to ensure no err on cmd.Run()

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			// access fields of exitErr here
			os.Exit(exitErr.ExitCode())
		} else {
			fmt.Printf("Err: %v", exitErr)
			os.Exit(1)
		}
	}

	//output, err := cmd.Output()
	//if err != nil {
	//	fmt.Printf("Err: %v", err)
	//	os.Exit(1)
	//}
	//
	//fmt.Println(string(output))

	// Your task is to implement a very basic version of docker run. It will be executed similar to docker run:
	//
	//mydocker run ubuntu:latest /usr/local/bin/docker-explorer echo h
}

func copyCommandExecutableToTempDir(tempDirPath string, command string) error {
	sourceFile := command
	input, err := ioutil.ReadFile(sourceFile)
	if err != nil {
		fmt.Printf("read - Err: %v", err)
		return err
	}

	destinationFile := path.Join(tempDirPath, sourceFile)
	err = os.MkdirAll(tempDirPath+"/usr/local/bin", os.ModeDir)
	if err != nil {
		fmt.Printf("MkdirAll - Err: %v", err)
		return err
	}
	err = ioutil.WriteFile(destinationFile, input, os.ModePerm)
	if err != nil {
		fmt.Printf("write - Err: %v", err)
		return err
	}

	return nil
}

func createDevNull(tempDirPath string) error {
	devNullPath := path.Join(tempDirPath, "dev")
	if err := os.MkdirAll(devNullPath, os.ModeDir); err != nil {
		return err
	}

	return ioutil.WriteFile(path.Join(tempDirPath, "dev", "null"), []byte{}, os.ModePerm)
}
