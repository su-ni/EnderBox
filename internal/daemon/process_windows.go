//go:build windows

package daemon

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type jobHandle = windows.Handle

// assignToJob 把进程挂进 Job Object。
// 没有这一步，cmd.Process.Kill() 只杀 java.exe，崩遗留的子进程会一直占着端口。
func assignToJob(p *os.Process) (windows.Handle, error) {
	if p == nil {
		return 0, os.ErrInvalid
	}
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_INFORMATION,
		false, uint32(p.Pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}

	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func terminateJob(job windows.Handle) {
	if job != 0 {
		_ = windows.TerminateJobObject(job, 1)
	}
}

func closeJob(job windows.Handle) {
	if job != 0 {
		_ = windows.CloseHandle(job)
	}
}
