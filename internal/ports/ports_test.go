package ports

import (
	"net"
	"strconv"
	"testing"

	"github.com/jackall3n/run-stack/internal/state"
)

func TestAllocateIsStickyAndAvoidsOtherStacks(t *testing.T) {
	current := &state.State{Stacks: map[string]*state.Entry{}}
	names := []string{"WEB_PORT", "JOBS_PORT"}
	portRange := [2]int{42000, 42999}

	first, _, err := Allocate(current, "shop", names, portRange)
	if err != nil {
		t.Fatal(err)
	}

	current.Stacks["shop"] = &state.Entry{ID: "shop", Ports: first}

	again, moved, _ := Allocate(current, "shop", names, portRange)

	if again["WEB_PORT"] != first["WEB_PORT"] || again["JOBS_PORT"] != first["JOBS_PORT"] || len(moved) != 0 {
		t.Errorf("ports changed between runs: %v then %v", first, again)
	}

	other, _, _ := Allocate(current, "shop-feature", names, portRange)

	for _, port := range other {
		if port == first["WEB_PORT"] || port == first["JOBS_PORT"] {
			t.Errorf("another stack was given a port already recorded: %v vs %v", other, first)
		}
	}

	// Something else takes the stack's port while it is stopped: it must move.
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(first["WEB_PORT"]))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	after, moved, _ := Allocate(current, "shop", names, portRange)

	if after["WEB_PORT"] == first["WEB_PORT"] || len(moved) != 1 {
		t.Errorf("a taken port was kept: %v, moved %v", after, moved)
	}
}
