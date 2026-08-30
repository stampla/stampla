package exif

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// stderrGrace covers only scheduling: -echo4 is written before
	// {ready}, so a command's fence is already in the pipe by the
	// time its sentinel arrives on stdout.
	stderrGrace = 10 * time.Second

	// shutdownTimeout is how long a process gets to exit on its own
	// before it is killed.
	shutdownTimeout = 10 * time.Second

	// noteBuffer holds a batch's stderr. Sized well past a chunk of
	// refusals; anything beyond it is dropped rather than allowed to
	// stall the process, and those files fall back to a generic error.
	noteBuffer = 4 * chunkSize

	// maxNoteLine bounds one stderr line.
	maxNoteLine = 64 * 1024

	// notesReported bounds how much stderr an error quotes.
	notesReported = 10
)

var errExited = errors.New("exiftool exited unexpectedly")

// worker is one persistent ExifTool process speaking -stay_open.
type worker struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	// notes carries diagnostic stderr and is lossy on purpose; fences
	// carries the -echo4 marker that ends each command's stderr and
	// is never dropped. Neither send blocks, so no amount of output
	// can wedge the process.
	notes   chan string
	fences  chan int
	drained chan struct{}

	// mu gives one command at a time the process, and guards the rest.
	mu      sync.Mutex
	seq     int
	dead    error
	stopped bool
}

func startWorker(argv []string) (*worker, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("exiftool stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("exiftool stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("exiftool stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting exiftool: %w", err)
	}
	w := &worker{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReaderSize(stdout, 64*1024),
		notes:   make(chan string, noteBuffer),
		fences:  make(chan int, 1),
		drained: make(chan struct{}),
	}
	go w.pump(stderr)
	return w, nil
}

// execute runs one command and returns its stdout together with the
// stderr it produced. A process that has failed once is never used
// again: its error is the answer to every later command.
func (w *worker) execute(args []string, timeout time.Duration) (string, []string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dead != nil {
		return "", nil, w.dead
	}
	w.discardStale()

	w.seq++
	body, err := payload(args, w.seq)
	if err != nil {
		return "", nil, err
	}
	if _, err := w.stdin.Write(body); err != nil {
		return "", nil, w.die(fmt.Errorf("exiftool is gone: %w", err))
	}

	type answer struct {
		text string
		err  error
	}
	// The read runs in its own goroutine so a process that never
	// answers costs a deadline rather than the caller's run; killing
	// it on the way out is what ends the goroutine.
	done := make(chan answer, 1)
	fence := fenceFor(w.seq)
	go func() {
		text, err := readTo(w.stdout, fence)
		done <- answer{text, err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case got := <-done:
		w.awaitFence(w.seq)
		notes := w.takeNotes()
		if got.err != nil {
			return "", notes, w.die(fmt.Errorf("%w: %s", got.err, summarize(notes)))
		}
		return got.text, notes, nil
	case <-timer.C:
		notes := w.takeNotes()
		return "", notes, w.die(fmt.Errorf(
			"exiftool did not respond within %s: %s", timeout, summarize(notes)))
	}
}

// payload renders one command. The -stay_open protocol is one argument
// per line, so a newline inside an argument would smuggle further
// arguments — tag writes among them — into the batch.
func payload(args []string, seq int) ([]byte, error) {
	var body bytes.Buffer
	// -echo4 marks the end of this command's stderr, which is
	// otherwise a stream with no boundaries.
	for _, arg := range append([]string{"-echo4", fenceFor(seq)}, args...) {
		if strings.ContainsAny(arg, "\n\r") {
			return nil, fmt.Errorf("%w: %q", ErrNewlineInPath, arg)
		}
		body.WriteString(arg)
		body.WriteByte('\n')
	}
	fmt.Fprintf(&body, "-execute%d\n", seq)
	return body.Bytes(), nil
}

func fenceFor(seq int) string { return "{ready" + strconv.Itoa(seq) + "}" }

func parseFence(line string) (int, bool) {
	rest, ok := strings.CutPrefix(line, "{ready")
	if !ok {
		return 0, false
	}
	digits, ok := strings.CutSuffix(rest, "}")
	if !ok {
		return 0, false
	}
	seq, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return seq, true
}

// readTo returns everything before the sentinel line. Line endings may
// be \n or \r\n depending on the platform ExifTool runs on.
func readTo(r *bufio.Reader, fence string) (string, error) {
	var text strings.Builder
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			if strings.TrimRight(line, "\r\n") == fence {
				return text.String(), nil
			}
			text.WriteString(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return text.String(), errExited
			}
			return text.String(), err
		}
	}
}

// pump moves stderr off the pipe as fast as ExifTool writes it.
func (w *worker) pump(r io.Reader) {
	defer close(w.drained)
	defer close(w.fences)
	lines := bufio.NewScanner(r)
	lines.Buffer(make([]byte, 0, 4096), maxNoteLine)
	for lines.Scan() {
		line := strings.TrimRight(lines.Text(), "\r")
		if seq, ok := parseFence(line); ok {
			select {
			case w.fences <- seq:
			default:
			}
			continue
		}
		select {
		case w.notes <- line:
		default:
		}
	}
}

// awaitFence waits for the current command's stderr to be complete.
// Losing the race is harmless: the JSON answer is authoritative, and
// only the wording of a per-file error depends on stderr.
func (w *worker) awaitFence(seq int) {
	timer := time.NewTimer(stderrGrace)
	defer timer.Stop()
	for {
		select {
		case reached, ok := <-w.fences:
			if !ok || reached >= seq {
				return
			}
		case <-timer.C:
			return
		}
	}
}

func (w *worker) takeNotes() []string {
	var notes []string
	for {
		select {
		case note := <-w.notes:
			notes = append(notes, note)
		default:
			return notes
		}
	}
}

// discardStale drops anything left over from an abandoned command so
// it cannot be read as this one's.
func (w *worker) discardStale() {
	for {
		select {
		case _, ok := <-w.fences:
			if !ok {
				w.takeNotes()
				return
			}
		case <-w.notes:
		default:
			return
		}
	}
}

func (w *worker) die(cause error) error {
	if w.dead == nil {
		w.dead = cause
	}
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	return w.dead
}

// stop ends the process, gracefully if it will go. It reports only a
// process that had to be killed after refusing to exit; a process that
// already failed was reported to whoever's read it failed.
func (w *worker) stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return nil
	}
	w.stopped = true
	if w.dead == nil {
		_, _ = w.stdin.Write([]byte("-stay_open\nFalse\n"))
	}
	_ = w.stdin.Close()

	// Waiting for stderr to end before reaping keeps the last of it
	// out of the race with the pipes Wait closes.
	reaped := make(chan struct{})
	go func() {
		defer close(reaped)
		<-w.drained
		_ = w.cmd.Wait()
	}()

	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	select {
	case <-reaped:
		return nil
	case <-timer.C:
	}
	_ = w.cmd.Process.Kill()
	<-reaped
	return fmt.Errorf("exiftool did not exit within %s and was killed", shutdownTimeout)
}

func summarize(notes []string) string {
	if len(notes) == 0 {
		return "(no stderr output)"
	}
	if len(notes) > notesReported {
		notes = notes[len(notes)-notesReported:]
	}
	return strings.Join(notes, "; ")
}
