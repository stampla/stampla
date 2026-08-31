package cli

import (
	"encoding/json"

	"github.com/stampla/stampla/internal/engine"
	"github.com/stampla/stampla/internal/finding"
	"github.com/stampla/stampla/internal/layout"
)

// porcelainFormat is the version of the machine interface. A consumer
// that does not know a format refuses the stream rather than guessing at
// it, which is why it rides on the first line of every run.
const porcelainFormat = 1

// The porcelain events. Field order is struct order and every field is
// always written, empty rather than absent: this is a contract another
// program is built against, so a shape that varies with the data would
// be a shape nobody can parse once and for all.
type (
	planEvent struct {
		Type   string `json:"type"`
		Format int    `json:"format"`
		Mode   string `json:"mode"`
		Dest   string `json:"dest"`
		Layout string `json:"layout"`
		Source string `json:"source"`
	}

	findingEvent struct {
		Type   string        `json:"type"`
		Class  finding.Class `json:"class"`
		Path   string        `json:"path"`
		Old    string        `json:"old"`
		New    string        `json:"new"`
		Detail string        `json:"detail"`
	}

	progressEvent struct {
		Type  string       `json:"type"`
		Phase engine.Phase `json:"phase"`
		Done  int          `json:"done"`
		Total int          `json:"total"`
	}

	resultEvent struct {
		Type    string `json:"type"`
		Exit    int    `json:"exit"`
		Applied int    `json:"applied"`
		Failed  int    `json:"failed"`
		Receipt string `json:"receipt"`
		Marker  string `json:"marker"`
	}
)

// porcelain writes the run as NDJSON, one object per line.
type porcelain struct {
	enc *json.Encoder
}

func newPorcelain(stdout *out) *porcelain {
	enc := json.NewEncoder(stdout.w)
	// Paths are not HTML. Escaping them would leave a consumer reading
	// & where the filesystem has an ampersand.
	enc.SetEscapeHTML(false)
	return &porcelain{enc: enc}
}

// emit writes one event. The error is dropped for the same reason every
// other write here drops it: there is nowhere left to report a broken
// stdout to, and a run whose files have landed must not fail because
// nobody was listening.
func (p *porcelain) emit(event any) { _ = p.enc.Encode(event) }

func (p *porcelain) head(mode engine.Mode, root string, _ bool, res layout.Resolution) {
	// One plan object per archive examined, so a stream that descends
	// into nested roots says which archive each finding belongs to and
	// under which declaration it was judged.
	p.emit(planEvent{
		Type:   "plan",
		Format: porcelainFormat,
		Mode:   mode.String(),
		Dest:   root,
		Layout: res.Pattern.String(),
		Source: res.Source,
	})
}

func (p *porcelain) body(a archive) {
	// Every finding, converged ones included: a program is not reading a
	// report, it is reading an inventory, and the class it does not care
	// about is cheaper to skip than to ask for.
	for _, f := range a.plan.Findings {
		path := f.Path
		if path == "" {
			path = f.Old
		}
		p.emit(findingEvent{
			Type: "finding", Class: f.Class, Path: path,
			Old: f.Old, New: f.New, Detail: f.Detail,
		})
	}
}

func (p *porcelain) progress(phase engine.Phase, done, total int, _ string) {
	p.emit(progressEvent{Type: "progress", Phase: phase, Done: done, Total: total})
}

func (p *porcelain) tail(o outcome) {
	p.emit(resultEvent{
		Type: "result", Exit: o.exit, Applied: o.applied, Failed: o.failed,
		Receipt: o.receipt, Marker: o.marker,
	})
}
