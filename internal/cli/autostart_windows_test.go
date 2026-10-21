//go:build windows

package cli

import "testing"

func TestDaemonCommandBreakaway(t *testing.T) {
	com := daemonCommand(`C:\bin\central.exe`, `C:\dados`, true)
	if com.SysProcAttr.CreationFlags&createBreakawayFromJob == 0 {
		t.Fatalf("flags = %#x, falta CREATE_BREAKAWAY_FROM_JOB", com.SysProcAttr.CreationFlags)
	}
	sem := daemonCommand(`C:\bin\central.exe`, `C:\dados`, false)
	if sem.SysProcAttr.CreationFlags&createBreakawayFromJob != 0 {
		t.Fatalf("flags = %#x, não devia ter CREATE_BREAKAWAY_FROM_JOB", sem.SysProcAttr.CreationFlags)
	}
	if sem.SysProcAttr.CreationFlags&detachedProcess == 0 {
		t.Fatalf("flags = %#x, falta DETACHED_PROCESS", sem.SysProcAttr.CreationFlags)
	}
}
