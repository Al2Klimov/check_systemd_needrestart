package main

import (
	"bytes"
	"context"
	"golang.org/x/sync/semaphore"
	"os"
	"os/exec"
	"runtime"
	"sync"
)

type exePathInfo struct {
	path  string
	mutex sync.RWMutex
}

var exePaths = map[string]*exePathInfo{}
var exePathsMutex sync.RWMutex
var execSemaphore = semaphore.NewWeighted(int64(runtime.NumCPU()) * 2)
var execSemaphoreContext = context.Background()
var singleQuote = []byte("'")
var quotedSingleQuote = []byte(`'"'"'`)
var space = []byte(" ")

func system(exe string, args ...string) (effCmd string, out []byte, err error) {
	exePathsMutex.RLock()
	pathInfo, hasPath := exePaths[exe]
	exePathsMutex.RUnlock()

	if !hasPath {
		exePathsMutex.Lock()

		if pathInfo, hasPath = exePaths[exe]; !hasPath {
			pathInfo = &exePathInfo{path: ""}
			exePaths[exe] = pathInfo
		}

		exePathsMutex.Unlock()
	}

	pathInfo.mutex.RLock()
	exePath := pathInfo.path
	pathInfo.mutex.RUnlock()

	if exePath == "" {
		pathInfo.mutex.Lock()

		if exePath = pathInfo.path; exePath == "" {
			path, errLP := exec.LookPath(exe)
			if errLP != nil {
				pathInfo.mutex.Unlock()
				return formatCmd(exe, args...), nil, errLP
			}

			exePath = path
			pathInfo.path = exePath
		}

		pathInfo.mutex.Unlock()
	}

	cmd := exec.Command(exePath, args...)
	outBuf := bytes.Buffer{}

	cmd.Env = []string{"LC_ALL=C"}
	cmd.Dir = "/"
	cmd.Stdin = nil
	cmd.Stdout = &outBuf
	cmd.Stderr = os.Stderr

	if errRun := runCmd(cmd); errRun != nil {
		return formatCmd(exePath, args...), nil, errRun
	}

	return formatCmd(exePath, args...), outBuf.Bytes(), nil
}

func formatCmd(exe string, args ...string) string {
	argList := make([][]byte, 1+len(args))

	for i, arg := range append([]string{exe}, args...) {
		replaced := bytes.Replace([]byte(arg), singleQuote, quotedSingleQuote, -1)
		quoted := make([]byte, len(replaced)+2)

		quoted[0] = singleQuote[0]
		copy(quoted[1:], replaced)
		quoted[len(quoted)-1] = singleQuote[0]

		argList[i] = quoted
	}

	return string(bytes.Join(argList, space))
}

func runCmd(cmd *exec.Cmd) (err error) {
	execSemaphore.Acquire(execSemaphoreContext, 1)
	err = cmd.Run()
	execSemaphore.Release(1)

	return
}
