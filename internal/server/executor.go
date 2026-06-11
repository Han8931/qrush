package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/han/qrush/internal/protocol"
)

type Executor struct {
	logDir          string
	logDirMu        sync.RWMutex
	onFinishCommand string
	processes       sync.Map
}

type ExecRequest struct {
	Job      *Job
	JobQueue *JobQueue
	OnFinish func(jobID int, result protocol.Result)
}

func NewExecutor(logDir string) *Executor {
	return &Executor{logDir: logDir}
}

func (e *Executor) SetLogDir(dir string) {
	e.logDirMu.Lock()
	defer e.logDirMu.Unlock()
	e.logDir = dir
}

func (e *Executor) LogDir() string {
	e.logDirMu.RLock()
	defer e.logDirMu.RUnlock()
	return e.logDir
}

func (e *Executor) SetOnFinishCommand(command string) {
	e.logDirMu.Lock()
	defer e.logDirMu.Unlock()
	e.onFinishCommand = command
}

func (e *Executor) OutputPathFor(jobID int, logfile string) string {
	if logfile != "" {
		return logfile
	}
	e.logDirMu.RLock()
	logDir := e.logDir
	e.logDirMu.RUnlock()
	return filepath.Join(logDir, fmt.Sprintf("ru_%d_%s.out", jobID, randSuffix()))
}

func randSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (e *Executor) Run(req ExecRequest) {
	job := req.Job
	args := job.CommandArgs
	if len(args) == 0 {
		args = parseCommand(job.Info.Command)
	}
	if len(args) == 0 {
		req.OnFinish(job.ID, protocol.Result{ExitCode: -1})
		return
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = job.WorkDir
	if len(job.Environment) > 0 {
		cmd.Env = job.Environment
	}

	usePTY := job.Info.StoreOutput && !job.SeparateStderr
	if !usePTY {
		setSysProcAttr(cmd)
	}

	var outputFile string
	var outFile *os.File
	var ptmx *os.File

	if job.Info.StoreOutput {
		outputFile = job.Info.OutputFilename
		if outputFile == "" {
			outputFile = e.OutputPathFor(job.ID, job.Logfile)
		}

		var err error
		outFile, err = os.Create(outputFile)
		if err != nil {
			req.OnFinish(job.ID, protocol.Result{ExitCode: -1})
			return
		}

		if job.Info.Message != "" {
			fmt.Fprintf(outFile, "# %s\n\n", job.Info.Message)
		}

		if job.SeparateStderr {
			errFile, err := os.Create(outputFile + ".e")
			if err == nil {
				cmd.Stderr = errFile
				defer errFile.Close()
			}
		}
	}

	start := time.Now()

	if usePTY {
		var err error
		ptmx, err = pty.Start(cmd)
		if err != nil {
			fmt.Fprintf(outFile, "ru: failed to start: %v\n", err)
			outFile.Close()
			req.OnFinish(job.ID, protocol.Result{ExitCode: -1})
			return
		}
		go io.Copy(outFile, ptmx)
	} else {
		if job.Info.StoreOutput {
			cmd.Stdout = outFile
		}
		if err := cmd.Start(); err != nil {
			if outFile != nil {
				fmt.Fprintf(outFile, "ru: failed to start: %v\n", err)
				outFile.Close()
			}
			req.OnFinish(job.ID, protocol.Result{ExitCode: -1})
			return
		}
	}

	// Update job PID and output file through the queue's lock
	if req.JobQueue != nil {
		req.JobQueue.SetRunning(job.ID, cmd.Process.Pid, outputFile)
	}
	e.processes.Store(job.ID, cmd.Process)

	err := cmd.Wait()

	e.processes.Delete(job.ID)
	if ptmx != nil {
		ptmx.Close()
	}
	if outFile != nil {
		outFile.Close()
	}

	elapsed := time.Since(start)
	result := protocol.Result{
		RealTimeMS: elapsed.Milliseconds(),
	}

	if cmd.ProcessState != nil {
		result.UserTimeMS = cmd.ProcessState.UserTime().Milliseconds()
		result.SystemTimeMS = cmd.ProcessState.SystemTime().Milliseconds()
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			fillSignalInfo(&result, exitErr.ProcessState)
		} else {
			result.ExitCode = -1
		}
	}

	req.OnFinish(job.ID, result)
	e.runOnFinishHook(job, result)
}

func (e *Executor) Kill(jobID int) error {
	proc, ok := e.processes.Load(jobID)
	if !ok {
		return fmt.Errorf("job %d not running", jobID)
	}
	p := proc.(*os.Process)
	return killProcessGroup(p.Pid)
}

func (e *Executor) KillAll() {
	e.processes.Range(func(key, value interface{}) bool {
		p := value.(*os.Process)
		killProcessGroup(p.Pid)
		return true
	})
}

func (e *Executor) runOnFinishHook(job *Job, result protocol.Result) {
	e.logDirMu.RLock()
	hook := e.onFinishCommand
	e.logDirMu.RUnlock()
	if hook == "" {
		return
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-c", hook)
	cmd.Dir = job.WorkDir
	cmd.Env = append([]string{}, job.Environment...)
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("QRUSH_JOB_ID=%d", job.ID),
		fmt.Sprintf("QRUSH_EXIT_CODE=%d", result.ExitCode),
		fmt.Sprintf("QRUSH_OUTPUT=%s", job.Info.OutputFilename),
		fmt.Sprintf("TS_JOBID=%d", job.ID),
		fmt.Sprintf("TS_EXIT_CODE=%d", result.ExitCode),
		fmt.Sprintf("TS_OUTPUT=%s", job.Info.OutputFilename),
	)
	if err := cmd.Start(); err != nil {
		return
	}
	// Reap the hook process so it doesn't linger as a zombie. We don't block
	// the caller on it, so wait in the background and discard the result.
	go func() { _ = cmd.Wait() }()
}

func parseCommand(cmd string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range cmd {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if r == ' ' && !inSingle && !inDouble {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
