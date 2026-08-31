package cli

import (
	"strings"
	"testing"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/layout"
)

func TestTripwires(t *testing.T) {
	flag := func(s string) *string { return &s }

	tests := []struct {
		name string
		in   confirmInput
		want []string
	}{
		{
			name: "an ordinary import asks nothing",
			in: confirmInput{
				mode: engine.Copy,
				plan: fakePlan(engine.Copy),
			},
		},
		{
			name: "--layout contradicting the marker",
			in: confirmInput{
				mode:   engine.Copy,
				plan:   planWith(engine.Copy, flagOverriding("{yyyy}/{yyyy}-{mm}", "{yyyy}")),
				layout: flag("{yyyy}"),
			},
			want: []string{"layout"},
		},
		{
			// The flag repeating what the marker already says is not a
			// contradiction, whatever route the two took to get here.
			name: "--layout agreeing with the marker",
			in: confirmInput{
				mode:   engine.Copy,
				plan:   planWith(engine.Copy, flagOverriding("{yyyy}", "{yyyy}")),
				layout: flag("{yyyy}"),
			},
		},
		{
			name: "--layout with no marker to contradict",
			in: confirmInput{
				mode:   engine.Copy,
				plan:   fakePlan(engine.Copy),
				layout: flag("{yyyy}"),
			},
		},
		{
			name: "a mass reorganization",
			in: confirmInput{
				mode: engine.Move,
				plan: touching(fakePlan(engine.Move), 412, 500),
			},
			want: []string{"reorganize"},
		},
		{
			// Both halves have to trip: half of a small archive is a
			// handful of files, and a hundred files out of a hundred
			// thousand is an ordinary afternoon.
			name: "most of a small archive",
			in: confirmInput{
				mode: engine.Move,
				plan: touching(fakePlan(engine.Move), 40, 50),
			},
		},
		{
			name: "many files but a small share",
			in: confirmInput{
				mode: engine.Move,
				plan: touching(fakePlan(engine.Move), 400, 5000),
			},
		},
		{
			name: "a catalog beside an mv",
			in: confirmInput{
				mode: engine.Move,
				plan: withDAM(fakePlan(engine.Move), "/photos/Lightroom.lrcat"),
			},
			want: []string{"dam"},
		},
		{
			// cp adds a second copy the catalog does not know about, which
			// is nothing like moving the file it does know about.
			name: "a catalog beside a cp",
			in: confirmInput{
				mode: engine.Copy,
				plan: withDAM(fakePlan(engine.Copy), "/photos/Lightroom.lrcat"),
			},
		},
		{
			name: "removable source on mv",
			in: confirmInput{
				mode:      engine.Move,
				plan:      fakePlan(engine.Move),
				removable: "/Volumes/NIKON D850",
			},
			want: []string{"removable"},
		},
		{
			name: "removable source on cp",
			in: confirmInput{
				mode:      engine.Copy,
				plan:      fakePlan(engine.Copy),
				removable: "/Volumes/NIKON D850",
			},
		},
		{
			// Every predicate is evaluated before any of them is asked, and
			// they are asked in one fixed order.
			name: "all four at once",
			in: confirmInput{
				mode: engine.Move,
				plan: withDAM(touching(planWith(engine.Move,
					flagOverriding("{yyyy}/{yyyy}-{mm}", "Capture")), 412, 500),
					"/photos/Lightroom.lrcat"),
				layout:    flag("Capture"),
				removable: "/Volumes/NIKON D850",
			},
			want: []string{"layout", "reorganize", "dam", "removable"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var names []string
			for _, wire := range tripwires(tc.in) {
				names = append(names, wire.name)
				if wire.reason == "" {
					t.Errorf("the %s tripwire fired without saying why", wire.name)
				}
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Errorf("tripwires() = %v, want %v", names, tc.want)
			}
		})
	}
}

// TestTripwireEvidence pins what each prompt has to carry: a
// confirmation that does not quote its evidence is a confirmation
// answered without reading.
func TestTripwireEvidence(t *testing.T) {
	asked := "{yyyy}"
	wires := tripwires(confirmInput{
		mode: engine.Move,
		plan: withDAM(touching(planWith(engine.Move,
			flagOverriding("{yyyy}/{yyyy}-{mm}", asked)), 412, 500),
			"/photos/Lightroom.lrcat"),
		layout:    &asked,
		removable: "/Volumes/NIKON D850",
	})
	reasons := make(map[string]string, len(wires))
	for _, wire := range wires {
		reasons[wire.name] = wire.reason
	}

	want := map[string][]string{
		"layout":     {"{yyyy}", "{yyyy}/{yyyy}-{mm}", "/photos/.stampla"},
		"reorganize": {"412", "500", "82%", "/photos"},
		"dam":        {"/photos/Lightroom.lrcat", "dam = \"lrc\""},
		"removable":  {"/Volumes/NIKON D850", "verified"},
	}
	for name, phrases := range want {
		reason, fired := reasons[name]
		if !fired {
			t.Fatalf("the %s tripwire did not fire", name)
		}
		for _, phrase := range phrases {
			if !strings.Contains(reason, phrase) {
				t.Errorf("the %s prompt does not mention %q:\n%s", name, phrase, reason)
			}
		}
	}
}

func TestDAMPromptNamesFiveAndCountsTheRest(t *testing.T) {
	var artifacts []string
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		artifacts = append(artifacts, "/photos/"+name+".lrcat")
	}
	wire, fired := damBeside(confirmInput{
		mode: engine.Move,
		plan: withDAM(fakePlan(engine.Move), artifacts...),
	})
	if !fired {
		t.Fatal("damBeside did not fire on seven catalogs")
	}
	if !strings.Contains(wire.reason, "and 2 more") {
		t.Errorf("the dam prompt does not count the artifacts it did not name:\n%s", wire.reason)
	}
	if strings.Contains(wire.reason, "/photos/f.lrcat") {
		t.Errorf("the dam prompt listed more than five artifacts:\n%s", wire.reason)
	}
}

func TestConfirmWithoutATerminal(t *testing.T) {
	e := envWithoutTerminal()
	err := e.confirm([]tripwire{{name: "dam", reason: "a catalog sits here"}})
	if err == nil {
		t.Fatal("confirm() with no terminal was accepted")
	}
	if !strings.Contains(err.Error(), "-y") {
		t.Errorf("confirm() = %v, want it to name the way through", err)
	}
}

func TestConfirmAnswers(t *testing.T) {
	tests := []struct {
		name    string
		answers string
		wantOK  bool
	}{
		{name: "y", answers: "y\n", wantOK: true},
		{name: "Y", answers: "Y\n", wantOK: true},
		// Only y and Y. A confirmation that also took "yes" would be a
		// confirmation guessing at the shapes of yes, and the shapes of
		// no are the ones that matter.
		{name: "yes", answers: "yes\n"},
		{name: "n", answers: "n\n"},
		{name: "empty line", answers: "\n"},
		{name: "end of input", answers: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := envWithTerminal(tc.answers)
			err := e.confirm([]tripwire{{name: "dam", reason: "why"}})
			if tc.wantOK && err != nil {
				t.Errorf("confirm() = %v, want it accepted", err)
			}
			if !tc.wantOK && err == nil {
				t.Error("confirm() accepted an answer that was not yes")
			}
			if !strings.Contains(e.prompted(), "proceed? [y/N]") {
				t.Errorf("confirm() asked no question:\n%s", e.prompted())
			}
		})
	}
}

// TestConfirmStopsAtTheFirstNo proves a declined question is the end of
// the run rather than the first of several.
func TestConfirmStopsAtTheFirstNo(t *testing.T) {
	e := envWithTerminal("n\ny\n")
	err := e.confirm([]tripwire{{name: "one", reason: "first"}, {name: "two", reason: "second"}})
	if err == nil {
		t.Fatal("confirm() accepted a no")
	}
	if strings.Contains(e.prompted(), "second") {
		t.Error("confirm() asked the second question after the first was declined")
	}
}

// Helpers that fabricate the plans the predicates read.

func planWith(mode engine.Mode, res layout.Resolution) *engine.Plan {
	plan := fakePlan(mode)
	plan.Resolution = res
	return plan
}

func touching(plan *engine.Plan, touched, underRoot int) *engine.Plan {
	plan.Touched, plan.UnderRoot = touched, underRoot
	return plan
}

func withDAM(plan *engine.Plan, artifacts ...string) *engine.Plan {
	plan.DAMArtifacts = artifacts
	return plan
}

// flagOverriding is what resolution looks like after --layout won over
// a destination that declares something else: the flag's pattern, and
// the marker still carried so a report can quote what it said.
func flagOverriding(declared, asked string) layout.Resolution {
	res := declaredIn(testDest, declared)
	res.Pattern = layout.MustParsePattern(asked)
	res.Source = layout.SourceFlag
	res.SourcePath = ""
	return res
}
