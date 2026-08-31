package engine

import (
	"crypto/md5"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"runtime"
	"sync"
)

// hashChunk is the read buffer for a whole-file digest. Large enough
// that a spinning disk streams rather than seeks, small enough that a
// worker per core does not cost real memory.
const hashChunk = 4 << 20

// md5Of streams a file and returns the lowercase hex MD5 of every byte
// of it.
//
// This is not the identity hash. Identity is ExifTool's ImageDataHash,
// taken over the image or video payload alone so that writing metadata
// never renames a file. This digest covers the whole file, which is what
// two different jobs need: proving a copy reproduced its source exactly,
// and standing in as the identity hash for a format ExifTool has no
// payload hash for — a substitution the reported provenance always names.
func md5Of(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	digest := md5.New()
	if err := streamInto(digest, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// streamInto copies a reader into a hash with one reused buffer.
func streamInto(h hash.Hash, r io.Reader) error {
	buf := make([]byte, hashChunk)
	_, err := io.CopyBuffer(h, r, buf)
	return err
}

// hashResult is one file's whole-file digest, or why it has none.
type hashResult struct {
	digest string
	err    error
}

// hashFiles computes the whole-file digest of every path, in parallel.
// A file that cannot be read carries its own error rather than aborting
// the batch: whether an unreadable file stops the run is the caller's
// judgement, and for a dying memory card the answer is that the rest
// still imports.
//
// Progress is reported in the order files finish, which is not the order
// they were given; only the count is meaningful.
func hashFiles(paths []string, workers int, progress ProgressFunc) map[string]hashResult {
	results := make(map[string]hashResult, len(paths))
	if len(paths) == 0 {
		return results
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	workers = min(workers, len(paths))

	var (
		mu   sync.Mutex
		done int
		wg   sync.WaitGroup
	)
	jobs := make(chan string)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				digest, err := md5Of(path)
				mu.Lock()
				results[path] = hashResult{digest: digest, err: err}
				done++
				progress.emit(PhaseHash, done, len(paths), path)
				mu.Unlock()
			}
		}()
	}
	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
	wg.Wait()
	return results
}
