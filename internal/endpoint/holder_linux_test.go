package endpoint

import (
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// A holder that was killed and not reaped (a container whose first process does not reap) is a zombie: kill(pid, 0)
// still succeeds for it, but it is gone as a holder (#1437).
func TestHolderGoneForAZombie(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer cmd.Wait() // reaped only at the end: until then the killed child is a zombie
	if holderGoneOS(strconv.Itoa(pid), "") {
		t.Fatal("a running process was judged gone")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !procZombie(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !procZombie(pid) {
		t.Fatal("the killed child never became a zombie")
	}
	if !holderGoneOS(strconv.Itoa(pid), "") {
		t.Fatal("a zombie was judged a live holder")
	}
}
