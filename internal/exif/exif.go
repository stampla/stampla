package exif

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors reported per file rather than by a call.
var (
	// ErrClosed is reported for every path of a Read on a closed pool.
	ErrClosed = errors.New("exiftool pool is closed")
	// ErrNewlineInPath refuses a path the -stay_open protocol cannot
	// carry; see the package documentation.
	ErrNewlineInPath = errors.New("path contains a newline")
	// ErrEmptyPath refuses the empty string, which would reach
	// ExifTool as a blank argument line.
	ErrEmptyPath = errors.New("path is empty")
	// ErrBadTag refuses a tag name that would reach ExifTool as
	// something other than a request to read that tag.
	ErrBadTag = errors.New("unusable tag name")
)

// Metadata is what one file yielded.
type Metadata struct {
	// Path is the path as it was passed to Read.
	Path string
	// Tags maps a family-0 group-qualified name ("EXIF:DateTimeOriginal")
	// to ExifTool's printed value. Nil when the file was never read.
	Tags map[string]string
	// ImageDataHash is the MD5 of the image or video payload alone,
	// lowercase hex. Empty means the format has no payload ExifTool
	// hashes, which is not a failure — Err distinguishes the two.
	ImageDataHash string
	// Err is this file's read failure. Tags may still hold whatever
	// ExifTool managed to read.
	Err error
}

const (
	// hashType pins the image-data hash algorithm; see the package
	// documentation for why it can never be left to a default.
	hashType = "MD5"

	// defaultPoolSize is enough to keep a fast disk busy without a
	// wall of Perl processes.
	defaultPoolSize = 8

	// chunkSize caps the paths carried by one -execute.
	chunkSize = 500

	// A chunk's deadline scales with its size: the image-data hash
	// reads every byte of every payload, so a chunk of large videos
	// legitimately takes minutes.
	chunkTimeout   = 2 * time.Minute
	perFileTimeout = 10 * time.Second

	probeTimeout = 30 * time.Second
)

// Available reports whether this machine has an ExifTool stampla can
// use. It resolves the executable, then reads the image-data hash of a
// fixture whose digest is known: the tag is not extracted by default,
// an unsupported -api imagehashtype is only a warning, and identities
// must be reproducible everywhere, so support is proven rather than
// inferred from a version number.
func Available() error {
	_, err := resolve()
	return err
}

func resolve() (string, error) {
	exe, err := exec.LookPath("exiftool")
	if err != nil {
		return "", fmt.Errorf("exiftool not found on PATH; install it with: %s", installHint())
	}
	if err := probe(exe); err != nil {
		return "", err
	}
	return exe, nil
}

// installHint names the usual way to get ExifTool on this OS.
func installHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install exiftool"
	case "linux":
		return "sudo apt install libimage-exiftool-perl (or your distribution's package)"
	case "windows":
		return "choco install exiftool, or download from https://exiftool.org/"
	default:
		return "see https://exiftool.org/"
	}
}

// probeImage is a 1x1 greyscale PNG. probeHash is the image-data hash
// ExifTool must report for it.
var (
	probeImage = []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x00, 0x00, 0x00, 0x00, 0x3a, 0x7e, 0x9b,
		0x55, 0x00, 0x00, 0x00, 0x0b, 0x49, 0x44, 0x41, 0x54, 0x78, 0xda, 0x62, 0x6a, 0x00, 0x0c, 0x00,
		0x00, 0x86, 0x00, 0x83, 0xb5, 0x8e, 0xf2, 0xf6, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
		0xae, 0x42, 0x60, 0x82,
	}
	probeHash = "b27f0a24ca376c51cfbe196b48300cc7"
)

func probe(exe string) error {
	file, err := writeProbe()
	if err != nil {
		return probeVersion(exe)
	}
	defer func() { _ = os.Remove(file) }()

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	// The version comes back with the probe, for the error messages
	// below to quote.
	out, err := exec.CommandContext(ctx, exe, append(readArgs([]string{"ExifToolVersion"}), file)...).Output()
	if err != nil {
		return fmt.Errorf("running %s: %w (install it with: %s)", exe, err, installHint())
	}
	return checkProbe(exe, string(out))
}

func checkProbe(exe, out string) error {
	entries, err := parseEntries(out)
	if err != nil {
		return fmt.Errorf("%s did not answer a metadata read: %w", exe, err)
	}
	if len(entries) != 1 {
		return fmt.Errorf("%s answered a one-file read with %d results", exe, len(entries))
	}
	version := entries[0].tags["ExifTool:ExifToolVersion"]
	switch got := entries[0].hash; got {
	case probeHash:
		return nil
	case "":
		return fmt.Errorf(
			"%s (version %s) reports no ImageDataHash; stampla needs an ExifTool that supports it (install it with: %s)",
			exe, version, installHint())
	default:
		return fmt.Errorf(
			"%s (version %s) hashed the probe image to %s, not the expected %s %s: identities from this ExifTool would not match any other",
			exe, version, got, hashType, probeHash)
	}
}

func writeProbe() (string, error) {
	file, err := os.CreateTemp("", "stampla-exif-probe-*.png")
	if err != nil {
		return "", err
	}
	name := file.Name()
	_, writeErr := file.Write(probeImage)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// minVersion is the ExifTool release that introduced ImageDataHash.
const minVersion = 12.00

// probeVersion is the fallback when no probe file can be written —
// a read-only temp directory, most likely. Every release from
// minVersion on supports both ImageDataHash and the MD5 setting, so
// the version alone is taken as the answer.
func probeVersion(exe string) error {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "-ver").Output()
	if err != nil {
		return fmt.Errorf("running %s -ver: %w (install it with: %s)", exe, err, installHint())
	}
	text := strings.TrimSpace(string(out))
	version, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return fmt.Errorf("%s -ver printed %q, which is not a version", exe, text)
	}
	if version < minVersion {
		return fmt.Errorf(
			"exiftool %s is too old; stampla needs %.2f or newer for ImageDataHash (install it with: %s)",
			text, minVersion, installHint())
	}
	return nil
}

// Pool is a set of persistent ExifTool processes. Its zero value is
// not usable; call NewPool. A Pool is safe for concurrent use, and one
// process serves one chunk of one read at a time.
type Pool struct {
	workers []*worker

	// mu keeps Close from tearing down processes a Read is using.
	mu       sync.RWMutex
	closed   bool
	closeErr error

	// per-chunk deadline, overridden in tests
	base    time.Duration
	perFile time.Duration
}

// NewPool starts a pool of size ExifTool processes; size <= 0 asks for
// a sensible default. Processes are started eagerly, so a machine
// unable to run ExifTool fails here rather than on the first read.
func NewPool(size int) (*Pool, error) {
	exe, err := resolve()
	if err != nil {
		return nil, err
	}
	return newPool([]string{exe, "-stay_open", "True", "-@", "-"}, size)
}

func newPool(argv []string, size int) (*Pool, error) {
	if size <= 0 {
		size = min(defaultPoolSize, runtime.NumCPU())
	}
	size = max(size, 1)
	p := &Pool{base: chunkTimeout, perFile: perFileTimeout}
	for range size {
		w, err := startWorker(argv)
		if err != nil {
			_ = p.Close()
			return nil, err
		}
		p.workers = append(p.workers, w)
	}
	return p, nil
}

// Close shuts every process down, gracefully where it can and by
// killing where it cannot. It is idempotent, and reports only
// processes that had to be killed after refusing to exit.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return p.closeErr
	}
	p.closed = true
	var errs []error
	for _, w := range p.workers {
		errs = append(errs, w.stop())
	}
	p.closeErr = errors.Join(errs...)
	return p.closeErr
}

// Read returns one Metadata per path, in the order given, carrying the
// named tags. A bare name ("DateTimeOriginal") returns that tag from
// every group it appears in, which is what ranking needs; a qualified
// name ("EXIF:DateTimeOriginal") narrows it to one. An empty list asks
// for every tag, which is far more than any caller reads.
//
// A file that cannot be read carries its own error; the rest of the
// batch is unaffected. Large batches are sharded across the pool and
// read concurrently. Read never writes to or modifies any file.
func (p *Pool) Read(paths, tags []string) []Metadata {
	out := make([]Metadata, len(paths))
	for i, file := range paths {
		out[i].Path = file
	}
	if len(paths) == 0 {
		return out
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		for i := range out {
			out[i].Err = ErrClosed
		}
		return out
	}
	// One unusable tag name spoils every file's read, so none of them
	// is attempted.
	for _, tag := range tags {
		if err := checkTag(tag); err != nil {
			for i := range out {
				out[i].Err = err
			}
			return out
		}
	}

	// Paths a process must never see are answered here and left out
	// of every batch.
	queue := make([]int, 0, len(paths))
	for i, file := range paths {
		if err := checkPath(file); err != nil {
			out[i].Err = err
			continue
		}
		queue = append(queue, i)
	}
	if len(queue) == 0 {
		return out
	}

	base := readArgs(tags)
	chunks := shard(len(queue), len(p.workers))
	var wg sync.WaitGroup
	for n, w := range p.workers {
		// Chunks are dealt round robin, so every process gets a share
		// and each one works through its own without contending.
		var mine [][2]int
		for c := n; c < len(chunks); c += len(p.workers) {
			mine = append(mine, chunks[c])
		}
		if len(mine) == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, span := range mine {
				p.readChunk(w, base, queue[span[0]:span[1]], paths, out)
			}
		}()
	}
	wg.Wait()
	return out
}

// shard splits n paths into index spans: one per process while that
// keeps chunks under the cap, more when it does not.
func shard(n, workers int) [][2]int {
	if n <= 0 || workers < 1 {
		return nil
	}
	size := min((n+workers-1)/workers, chunkSize)
	spans := make([][2]int, 0, (n+size-1)/size)
	for start := 0; start < n; start += size {
		spans = append(spans, [2]int{start, min(start+size, n)})
	}
	return spans
}

// readChunk fills out[i] for every i in idx. Goroutines share out and
// base, but each holds a disjoint set of indices and appends the file
// list to its own copy.
func (p *Pool) readChunk(w *worker, base []string, idx []int, paths []string, out []Metadata) {
	args := slices.Clone(base)
	for _, i := range idx {
		args = append(args, protocolPath(paths[i]))
	}

	stdout, notes, err := w.execute(args, p.base+time.Duration(len(idx))*p.perFile)
	if err != nil {
		for _, i := range idx {
			out[i].Err = err
		}
		return
	}
	entries, err := parseEntries(stdout)
	if err != nil {
		for _, i := range idx {
			out[i].Err = err
		}
		return
	}

	found := make(map[string]*entry, len(entries))
	for _, e := range entries {
		if _, seen := found[e.key]; !seen {
			found[e.key] = e
		}
	}
	refused := attribute(notes)

	for _, i := range idx {
		key := normalize(paths[i])
		e, ok := found[key]
		if !ok {
			// ExifTool omits files it could not open; its stderr
			// says why.
			if why, said := refused[key]; said {
				out[i].Err = fmt.Errorf("exiftool: %s", why)
			} else {
				out[i].Err = fmt.Errorf("exiftool returned no metadata for %s", paths[i])
			}
			continue
		}
		out[i].Tags = e.take()
		out[i].ImageDataHash = e.hash
		out[i].Err = e.err
	}
}

// readArgs is the whole vocabulary a read speaks. It carries no write
// option, and the file list is appended to a copy of it.
func readArgs(tags []string) []string {
	args := make([]string, 0, len(tags)+12)
	args = append(args, "-j", "-a", "-G0", "-api", "imagehashtype="+hashType, "-ImageDataHash")
	// Error and Warning are tags, not output: left off the list, a
	// file ExifTool could not parse comes back as an empty result
	// instead of a failure.
	args = append(args, "-Error", "-Warning")
	if len(tags) == 0 {
		args = append(args, "-All")
	}
	for _, tag := range tags {
		args = append(args, "-"+tag)
	}
	if runtime.GOOS == "windows" {
		args = append(args, "-charset", "filename=UTF8")
	}
	return args
}

// checkTag guards the one argument a caller composes. A name reaches
// ExifTool as "-Name", so one carrying "=" would arrive as a tag
// assignment — a write — and one carrying a newline would open an
// argument of its own.
func checkTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("%w: the empty name", ErrBadTag)
	}
	if strings.ContainsAny(tag, "\n\r=") || strings.HasPrefix(tag, "-") {
		return fmt.Errorf("%w: %q", ErrBadTag, tag)
	}
	return nil
}

func checkPath(file string) error {
	if file == "" {
		return ErrEmptyPath
	}
	if strings.ContainsAny(file, "\n\r") {
		return fmt.Errorf("%w: %q", ErrNewlineInPath, file)
	}
	return nil
}

// protocolPath makes a path unmistakable as a file name: ExifTool
// reads a leading "-" as an option, which a relative path can carry.
func protocolPath(file string) string {
	if strings.HasPrefix(file, "-") {
		return "./" + file
	}
	return file
}

// normalize is the key both a requested path and the SourceFile
// ExifTool echoes back reduce to. ExifTool answers in forward slashes
// on every platform, and protocolPath may have added a "./".
func normalize(file string) string {
	return path.Clean(filepath.ToSlash(file))
}

// attribute maps the files ExifTool refused to the reason it gave.
// Per-file trouble reads "Error: <text> - <path>".
func attribute(notes []string) map[string]string {
	said := make(map[string]string)
	for _, note := range notes {
		text, ok := strings.CutPrefix(note, "Error: ")
		if !ok {
			continue
		}
		const sep = " - "
		at := strings.LastIndex(text, sep)
		if at < 0 {
			continue
		}
		said[normalize(text[at+len(sep):])] = text
	}
	return said
}

// entry is one JSON object of an ExifTool answer.
type entry struct {
	key   string
	tags  map[string]string
	hash  string
	err   error
	taken bool
}

// take hands out the tag map, copying it for the second and later
// claimants so a path repeated in one batch never shares one map.
func (e *entry) take() map[string]string {
	if e.taken {
		return maps.Clone(e.tags)
	}
	e.taken = true
	return e.tags
}

func parseEntries(stdout string) ([]*entry, error) {
	text := strings.TrimSpace(stdout)
	if text == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	// Numbers keep the text ExifTool printed rather than a float's
	// idea of it.
	decoder.UseNumber()
	var docs []map[string]any
	if err := decoder.Decode(&docs); err != nil {
		return nil, fmt.Errorf("unparsable exiftool output %q: %w", clip(text), err)
	}

	entries := make([]*entry, 0, len(docs))
	for _, doc := range docs {
		source, ok := doc["SourceFile"].(string)
		if !ok {
			continue
		}
		delete(doc, "SourceFile")
		e := &entry{key: normalize(source), tags: make(map[string]string, len(doc))}
		for name, value := range doc {
			e.tags[name] = tagValue(value)
		}
		e.hash = strings.ToLower(e.tags["File:ImageDataHash"])
		if why := e.tags["ExifTool:Error"]; why != "" {
			e.err = fmt.Errorf("exiftool: %s", why)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// tagValue flattens a JSON value to the text a tag ranking can compare.
// Structured tags keep their JSON so nothing is silently lost.
func tagValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(encoded)
	}
}

func clip(text string) string {
	const limit = 200
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
