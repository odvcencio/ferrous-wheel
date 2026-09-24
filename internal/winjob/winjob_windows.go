//go:build windows

// Package winjob keeps a Windows process and its descendants in one Job Object.
package winjob

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job owns a Windows Job Object. Closing it terminates every member.
type Job struct {
	mu     sync.Mutex
	handle windows.Handle
	killed bool
}

// Prepare makes the process start suspended, so it cannot create descendants
// before Attach places it in a Job Object. Call this before cmd.Start.
func Prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP
}

// Attach assigns a started, suspended process to a Job Object and resumes its
// initial thread. A failed attachment leaves the process suspended or killed.
func Attach(cmd *exec.Cmd) (_ *Job, resultErr error) {
	if cmd.Process == nil {
		return nil, errors.New("attach job: command has not started")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	defer func() {
		if resultErr != nil {
			if closeErr := windows.CloseHandle(job); closeErr != nil {
				resultErr = fmt.Errorf("%w; close failed: %v", resultErr, closeErr)
			}
		}
	}()

	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	runtime.KeepAlive(&limits)
	if err != nil {
		return nil, fmt.Errorf("set job kill-on-close: %w", err)
	}

	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, fmt.Errorf("open suspended process: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return nil, fmt.Errorf("assign process to job: %w", err)
	}

	threadID, err := initialThreadID(uint32(cmd.Process.Pid))
	if err != nil {
		return nil, err
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
	if err != nil {
		return nil, fmt.Errorf("open suspended thread: %w", err)
	}
	defer windows.CloseHandle(thread)
	previous, err := windows.ResumeThread(thread)
	if err != nil {
		return nil, fmt.Errorf("resume initial thread: %w", err)
	}
	if previous != 1 {
		return nil, fmt.Errorf("resume initial thread: unexpected suspend count %d", previous)
	}
	return &Job{handle: job}, nil
}

func initialThreadID(pid uint32) (uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, fmt.Errorf("snapshot process threads: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	err = windows.Thread32First(snapshot, &entry)
	var threadID uint32
	for err == nil {
		if entry.OwnerProcessID == pid {
			if threadID != 0 {
				return 0, errors.New("suspended process has more than one thread")
			}
			threadID = entry.ThreadID
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		err = windows.Thread32Next(snapshot, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return 0, fmt.Errorf("enumerate process threads: %w", err)
	}
	if threadID == 0 {
		return 0, errors.New("suspended process has no initial thread")
	}
	return threadID, nil
}

// Interrupt sends CTRL_BREAK_EVENT to the process group. The caller can use
// Kill when the child has no console or does not handle the event.
func (j *Job) Interrupt(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

// Kill terminates every process in the job.
func (j *Job) Kill() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return nil
	}
	if j.killed {
		return nil
	}
	if err := windows.TerminateJobObject(j.handle, 1); err != nil {
		return err
	}
	j.killed = true
	return nil
}

// Close terminates remaining descendants, waits briefly for their handles to
// close, and closes the job handle. KILL_ON_JOB_CLOSE is a final safety net.
func (j *Job) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return nil
	}
	handle := j.handle
	j.handle = 0
	var terminateErr error
	if !j.killed {
		terminateErr = windows.TerminateJobObject(handle, 1)
	}
	waitErr := waitForEmptyJob(handle, 2*time.Second)
	closeErr := windows.CloseHandle(handle)
	return combineErrors(combineErrors(terminateErr, waitErr), closeErr)
}

func combineErrors(first, second error) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return fmt.Errorf("%v; %w", first, second)
}

type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func waitForEmptyJob(handle windows.Handle, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var accounting jobAccounting
		if err := windows.QueryInformationJobObject(handle, windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			return fmt.Errorf("query job processes: %w", err)
		}
		if accounting.ActiveProcesses == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for job cleanup: %d processes remain", accounting.ActiveProcesses)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
