// Package proc kills a harness process together with every process it
// started, including those in other process groups (design 001: no tool
// process started by a turn outlives that turn's stop). KillTree does it at
// once; DescendantGroups and KillGroups split it for a harness that is
// allowed to exit on its own first.
package proc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// process is one row of the process table.
type process struct {
	pid, ppid, pgid int
}

// KillTree sends SIGKILL to the process group of pid (pid must lead its
// group) and to the process group of every descendant of pid. Descendants
// are listed before anything is killed, because once pid dies its children
// are reparented and can no longer be found from it. It does not wait.
func KillTree(pid int) {
	desc := descendantsOf(pid, processTable())
	syscall.Kill(-pid, syscall.SIGKILL)
	own := syscall.Getpgrp()
	for _, p := range desc {
		if p.pgid > 1 && p.pgid != own && p.pgid != pid {
			syscall.Kill(-p.pgid, syscall.SIGKILL)
		}
		syscall.Kill(p.pid, syscall.SIGKILL)
	}
}

// DescendantGroups returns the process groups of pid's descendants, other
// than pid's own group and the caller's. It must run while pid lives: once
// pid has exited its children are reparented and can no longer be found.
func DescendantGroups(pid int) []int {
	own := syscall.Getpgrp()
	seen := map[int]bool{}
	var groups []int
	for _, p := range descendantsOf(pid, processTable()) {
		if p.pgid > 1 && p.pgid != own && p.pgid != pid && !seen[p.pgid] {
			seen[p.pgid] = true
			groups = append(groups, p.pgid)
		}
	}
	return groups
}

// KillGroups sends SIGKILL to each process group in groups. It does not
// wait.
func KillGroups(groups []int) {
	for _, g := range groups {
		syscall.Kill(-g, syscall.SIGKILL)
	}
}

// processTable lists every process with its parent and process group. It
// returns nil when ps cannot be run.
func processTable() []process {
	// /bin/ps first: the caller's PATH need not contain it.
	ps := "/bin/ps"
	if _, err := os.Stat(ps); err != nil {
		ps = "ps"
	}
	out, err := exec.Command(ps, "-A", "-o", "pid=", "-o", "ppid=", "-o", "pgid=").Output()
	if err != nil {
		return nil
	}
	var table []process
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		var p process
		var err1, err2, err3 error
		p.pid, err1 = strconv.Atoi(f[0])
		p.ppid, err2 = strconv.Atoi(f[1])
		p.pgid, err3 = strconv.Atoi(f[2])
		if err1 == nil && err2 == nil && err3 == nil {
			table = append(table, p)
		}
	}
	return table
}

// descendantsOf returns the descendants of pid in table, parents before
// their children.
func descendantsOf(pid int, table []process) []process {
	children := map[int][]process{}
	for _, p := range table {
		children[p.ppid] = append(children[p.ppid], p)
	}
	var out []process
	queue := []int{pid}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range children[next] {
			out = append(out, c)
			queue = append(queue, c.pid)
		}
	}
	return out
}
