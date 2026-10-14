package proc

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestKillTree starts a shell in its own process group that starts a
// grandchild in another process group, then checks that KillTree ends both,
// even when PATH does not contain ps.
func TestKillTree(t *testing.T) {
	// The shell prints the pid of a sleep it moves into a new process group
	// (set -m gives each background job its own group), then waits.
	cmd := exec.Command("/bin/sh", "-c", "set -m; /bin/sleep 300 & echo $!; wait")
	// Harness processes run with the caller's PATH, which tests narrow.
	t.Setenv("PATH", t.TempDir())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, err := out.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })
	if pgid, _ := syscall.Getpgid(child); pgid == cmd.Process.Pid {
		t.Fatal("the grandchild shares the shell's process group")
	}

	KillTree(cmd.Process.Pid)
	cmd.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for alive(child) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived KillTree", child)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDescendants(t *testing.T) {
	table := []process{{pid: 10, ppid: 1}, {pid: 11, ppid: 10}, {pid: 12, ppid: 11}, {pid: 13, ppid: 1}, {pid: 14, ppid: 12}}
	got := descendantsOf(10, table)
	var pids []int
	for _, p := range got {
		pids = append(pids, p.pid)
	}
	if len(pids) != 3 || pids[0] != 11 || pids[1] != 12 || pids[2] != 14 {
		t.Errorf("descendants of 10 = %v, want [11 12 14]", pids)
	}
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
