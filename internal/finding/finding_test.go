package finding

import "testing"

func TestClassSemantics(t *testing.T) {
	for _, tt := range []struct {
		class   Class
		alarm   bool
		pending bool
	}{
		{Converged, false, false},
		{Misplaced, false, true},
		{Stale, false, true},
		{Corrupt, true, false},
		{TimeDrift, true, false},
		{Unresolvable, false, true},
		{Conflict, false, true},
		{Missing, false, true},
		{Incoming, false, true},
	} {
		if got := tt.class.Alarm(); got != tt.alarm {
			t.Errorf("%s.Alarm() = %v, want %v", tt.class, got, tt.alarm)
		}
		if got := tt.class.Pending(); got != tt.pending {
			t.Errorf("%s.Pending() = %v, want %v", tt.class, got, tt.pending)
		}
	}
}

func TestExitCode(t *testing.T) {
	f := func(classes ...Class) []Finding {
		fs := make([]Finding, len(classes))
		for i, c := range classes {
			fs[i] = Finding{Class: c}
		}
		return fs
	}
	for _, tt := range []struct {
		name     string
		findings []Finding
		want     int
	}{
		{"empty", nil, ExitConverged},
		{"all converged", f(Converged, Converged), ExitConverged},
		{"pending", f(Converged, Stale), ExitPending},
		{"alarm dominates pending", f(Stale, Corrupt, Missing), ExitAlarm},
		{"alarm alone", f(TimeDrift), ExitAlarm},
		{"alarm first short-circuits", f(Corrupt, Stale), ExitAlarm},
	} {
		if got := ExitCode(tt.findings); got != tt.want {
			t.Errorf("%s: ExitCode = %d, want %d", tt.name, got, tt.want)
		}
	}
}
