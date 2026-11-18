package job

import "testing"

func TestTransitions(t *testing.T) {
	states := []State{Queued, Running, Succeeded, Failed, Cancelled, TimedOut, "unknown"}
	for _, from := range states {
		for _, to := range states {
			want := from == Queued && to == Running || from == Running && (to == Succeeded || to == Failed || to == Cancelled || to == TimedOut)
			if CanTransition(from, to) != want {
				t.Errorf("%s -> %s", from, to)
			}
		}
	}
	for _, id := range []string{"../x", "", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "0000000000000000000000000000000z"} {
		if ValidID(id) {
			t.Errorf("accepted %q", id)
		}
	}
	if !ValidID(NewID()) {
		t.Fatal("generated invalid ID")
	}
}
